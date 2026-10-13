package measure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	// DefaultRPCPort and DefaultP2PPort are off the ports of scripts/localnet (31000-31099) and of a
	// stock CometBFT (26656, 26657), so a measurement can run beside either.
	DefaultRPCPort = 36657
	DefaultP2PPort = 36656
	statusPoll     = 50 * time.Millisecond
	// stopGrace is how long a node gets to stop after SIGTERM before it is killed.
	stopGrace = 5 * time.Minute
	logName   = "node.log"
)

// Node is a running oramad.
type Node struct {
	cmd   *exec.Cmd
	done  chan error
	Start time.Time
	RPC   string
	Home  string
}

// StartNode starts `oramad start` on a home, with the API and gRPC servers off and the RPC and P2P
// listeners on loopback ports. Its output goes to <home>/node.log.
func (o Oramad) StartNode(home string, rpcPort, p2pPort int) (*Node, error) {
	logFile, err := os.OpenFile(filepath.Join(home, logName), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open the node log: %w", err)
	}
	cmd := exec.Command(o.Bin, "start", "--home", home,
		"--rpc.laddr", "tcp://127.0.0.1:"+strconv.Itoa(rpcPort),
		"--p2p.laddr", "tcp://127.0.0.1:"+strconv.Itoa(p2pPort),
		"--api.enable=false", "--grpc.enable=false", "--grpc-web.enable=false")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	n := &Node{cmd: cmd, done: make(chan error, 1), Start: time.Now(), RPC: "127.0.0.1:" + strconv.Itoa(rpcPort), Home: home}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("failed to start oramad: %w", err)
	}
	go func() {
		n.done <- cmd.Wait()
		_ = logFile.Close()
	}()
	return n, nil
}

// Status is the part of CometBFT's /status the measurements read.
type Status struct {
	Height     int64
	CatchingUp bool
}

// StatusOnce reads /status; an error means the RPC server is not answering (yet).
func (n *Node) StatusOnce(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+n.RPC+"/status", nil)
	if err != nil {
		return Status{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()
	var body struct {
		Result struct {
			SyncInfo struct {
				Height     string `json:"latest_block_height"`
				CatchingUp bool   `json:"catching_up"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Status{}, fmt.Errorf("status answer is not JSON: %w", err)
	}
	h, err := strconv.ParseInt(body.Result.SyncInfo.Height, 10, 64)
	if err != nil {
		return Status{}, fmt.Errorf("status height %q: %w", body.Result.SyncInfo.Height, err)
	}
	return Status{Height: h, CatchingUp: body.Result.SyncInfo.CatchingUp}, nil
}

// WaitHeight polls until the node's latest height is at least height and returns how long after the
// node started that was. It fails if the node exits first or the budget passes; the error carries the
// tail of the node's log.
func (n *Node) WaitHeight(ctx context.Context, height int64, budget time.Duration) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	tick := time.NewTicker(statusPoll)
	defer tick.Stop()
	for {
		select {
		case err := <-n.done:
			n.done <- err
			return 0, fmt.Errorf("oramad exited before height %d: %v: %s", height, err, n.logTail())
		case <-ctx.Done():
			return 0, fmt.Errorf("height %d not reached within %s: %s", height, budget, n.logTail())
		case <-tick.C:
			if st, err := n.StatusOnce(ctx); err == nil && st.Height >= height {
				return time.Since(n.Start), nil
			}
		}
	}
}

// WaitRPC polls until /status answers and returns how long after the start that was.
func (n *Node) WaitRPC(ctx context.Context, budget time.Duration) (time.Duration, error) {
	return n.WaitHeight(ctx, 0, budget)
}

func (n *Node) logTail() string {
	raw, err := os.ReadFile(filepath.Join(n.Home, logName))
	if err != nil {
		return "(no node log: " + err.Error() + ")"
	}
	return tail(string(bytes.TrimSpace(raw)))
}

// Stop asks the node to stop with SIGTERM, waits for it, and kills it if it does not stop in time.
func (n *Node) Stop() error {
	if err := n.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("failed to signal oramad: %w", err)
	}
	select {
	case <-n.done:
		return nil
	case <-time.After(stopGrace):
		_ = n.cmd.Process.Kill()
		<-n.done
		return fmt.Errorf("oramad did not stop within %s and was killed", stopGrace)
	}
}
