package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/chain/client/node"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

const (
	storageName        = "storage-private"
	privateReplicas    = 3
	dealDurationEpochs = 3
	// dealPricePerEpoch is per replica, in norama: 0.001 ORAMA, so the escrow is 9 million norama.
	dealPricePerEpoch = "1000000"
	// txGas and txFee are what `orama storage create` submits with; the fee is well above gas times
	// the floor base fee of 1 norama.
	txGas             = 600_000
	txFee             = 1_500_000
	minPlaintextBytes = 4096
	dealScanLimit     = 5000
	putWait           = 6 * time.Minute
	acceptWait        = 10 * time.Minute
	epochWait         = 20 * time.Minute
	storagePoll       = 5 * time.Second
	keyFileMode       = 0o600
)

var sealLine = regexp.MustCompile(`^slot (\d+) root ([0-9a-f]{64})$`)

// parseSealOutput reads `orama storage seal`'s "slot N root <hex>" lines into slot -> root.
func parseSealOutput(out string) (map[int]string, error) {
	roots := map[int]string{}
	for _, line := range strings.Split(out, "\n") {
		m := sealLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		i, _ := strconv.Atoi(m[1])
		if _, dup := roots[i]; dup {
			return nil, fmt.Errorf("slot %d is printed twice", i)
		}
		roots[i] = m[2]
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no slot roots in the seal output: %q", out)
	}
	return roots, nil
}

// pieceSpecs are the --piece arguments of `orama storage create`: <root>:<bytes>, in slot order.
func pieceSpecs(roots map[int]string, sizes map[int]int64) ([]string, error) {
	idx := make([]int, 0, len(roots))
	for i := range roots {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	specs := make([]string, 0, len(idx))
	for _, i := range idx {
		size, ok := sizes[i]
		if !ok || size <= 0 {
			return nil, fmt.Errorf("slot %d has no size", i)
		}
		specs = append(specs, fmt.Sprintf("%s:%d", roots[i], size))
	}
	return specs, nil
}

// distinctProviders reports whether every slot is assigned, to distinct nodes and distinct
// operators (the rule for a PRIVATE deal).
func distinctProviders(slots []storagetypes.Slot) error {
	nodes, ops := map[string]bool{}, map[string]bool{}
	for _, s := range slots {
		if s.NodeId == "" {
			return fmt.Errorf("slot %d is not assigned to a node", s.Index)
		}
		if nodes[s.NodeId] {
			return fmt.Errorf("node %s holds two slots", s.NodeId)
		}
		if ops[s.Operator] {
			return fmt.Errorf("operator %s holds two slots", s.Operator)
		}
		nodes[s.NodeId], ops[s.Operator] = true, true
	}
	return nil
}

// challengeView is one challenge of one node in one epoch, for the deal under test.
type challengeView struct {
	Epoch  uint64
	NodeID string
	Proved bool
}

// proofsAccepted judges the challenges of a deal: every slot's node proved at least once, and no
// challenge of an epoch that has closed (an epoch below current) is left unproved.
func proofsAccepted(slotNodes []string, views []challengeView, current uint64) error {
	proved := map[string]bool{}
	for _, v := range views {
		if v.Proved {
			proved[v.NodeID] = true
		} else if v.Epoch < current {
			return fmt.Errorf("node %s left a challenge of closed epoch %d unproved", v.NodeID, v.Epoch)
		}
	}
	for _, n := range slotNodes {
		if !proved[n] {
			return fmt.Errorf("node %s has no accepted proof yet", n)
		}
	}
	return nil
}

// runOrama runs the `orama` CLI with extra environment and returns its stdout and stderr.
func runOrama(ctx context.Context, bin string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("orama %s: %w: %s", strings.Join(args[:min(2, len(args))], " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func writeSecret(path string, data []byte) error {
	return os.WriteFile(path, data, keyFileMode)
}

// findDealByNonce scans deal ids from 1 for the deal whose nonce is nonce.
func findDealByNonce(ctx context.Context, c *node.Client, nonce []byte) (uint64, error) {
	for id := uint64(1); id <= dealScanLimit; id++ {
		var resp storagetypes.QueryDealResponse
		err := c.Query(ctx, "/orama.storage.v1.Query/Deal", &storagetypes.QueryDealRequest{DealId: id}, &resp)
		var qerr *node.QueryError
		if errors.As(err, &qerr) && qerr.NotFound() {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("read deal %d: %w", id, err)
		}
		if bytes.Equal(resp.Deal.DealNonce, nonce) {
			return id, nil
		}
	}
	return 0, errors.New("the created deal is not on chain")
}

func currentEpoch(ctx context.Context, c *node.Client) (uint64, error) {
	var resp emissiontypes.QueryCurrentEpochResponse
	if err := c.Query(ctx, "/orama.emission.v1.Query/CurrentEpoch", &emissiontypes.QueryCurrentEpochRequest{}, &resp); err != nil {
		return 0, err
	}
	return resp.EpochState.CurrentEpoch, nil
}

func waitFor(ctx context.Context, limit time.Duration, poll func() (bool, error)) error {
	deadline := time.Now().Add(limit)
	var last error
	for {
		ok, err := poll()
		if ok {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return fmt.Errorf("after %s: %v", limit, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(storagePoll):
		}
	}
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func randomHex(n int) (string, []byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(b), b, nil
}
