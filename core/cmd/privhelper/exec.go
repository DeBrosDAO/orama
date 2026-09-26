package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/unitenv"
	"github.com/DeBrosOfficial/network/pkg/wireguard"
)

// toolPaths are where each external tool lives on the supported releases. The
// helper never consults PATH: it runs as root on behalf of another user.
var toolPaths = map[string][]string{
	privhelper.ToolSystemctl: {"/usr/bin/systemctl", "/bin/systemctl"},
	privhelper.ToolUFW:       {"/usr/sbin/ufw", "/sbin/ufw"},
	"wg":                     {"/usr/bin/wg", "/bin/wg"},
}

// maxToolOutput bounds what a tool may write back: the response has to fit in
// what the client reads (MaxRequestBytes), JSON escaping included.
const maxToolOutput = privhelper.MaxRequestBytes / 4

// cappedBuffer keeps the first limit bytes and notes that the rest was cut.
type cappedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room < len(p) {
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		b.truncated = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func (b *cappedBuffer) String() string {
	if b.truncated {
		return b.Buffer.String() + "\n[orama-privhelper: output truncated]\n"
	}
	return b.Buffer.String()
}

// commandTimeout bounds one tool run, inside the service's RuntimeMaxSec.
const commandTimeout = 5 * time.Minute

// wgInterface is the mesh interface.
const wgInterface = "wg0"

// execute runs a validated invocation and reports how it went.
func execute(inv privhelper.Invocation, input []byte) privhelper.Response {
	switch inv.Tool {
	case privhelper.ToolSystemctl:
		if dir := privhelper.DeploymentDirToVerify(inv); dir != "" {
			if err := verifyDeploymentDir(rootfs.At(privhelper.DeploymentsAnchor), dir, allowedUser); err != nil {
				return refusal(err)
			}
		}
		return runTool(inv.Tool, inv.Args)
	case privhelper.ToolUFW:
		return runTool(inv.Tool, inv.Args)
	case privhelper.ToolWireGuard:
		return wireGuard(inv.Args, input)
	case privhelper.ToolDeploy:
		return deploy(inv.Args, input)
	case privhelper.ToolUnitEnv:
		return unitEnv(inv.Args, input)
	default:
		return failure(fmt.Errorf("no executor for %s", inv.Tool))
	}
}

func wireGuard(args []string, input []byte) privhelper.Response {
	conf := wireguard.NewConf("")
	switch args[0] {
	case "persist-peers":
		peers, err := privhelper.ParsePersistInput(input)
		if err != nil {
			return refused(err)
		}
		for _, p := range peers {
			if err := notThisNode(p.AllowedIP); err != nil {
				return refused(err)
			}
		}
		if err := conf.PersistPeers(peers); err != nil {
			return failure(err)
		}
		return privhelper.Response{Output: fmt.Sprintf("persisted %d peers\n", len(peers))}

	case "remove-peer":
		return removePeer(conf, args[1])

	default: // add-peer <key> <endpoint> <allowed-ip>, already validated.
		p := wireguard.Peer{PublicKey: args[1], Endpoint: args[2], AllowedIP: args[3]}
		if err := notThisNode(p.AllowedIP); err != nil {
			return refused(err)
		}
		wgArgs := []string{"set", wgInterface, "peer", p.PublicKey, "allowed-ips", p.AllowedIP, "persistent-keepalive", "25"}
		if p.Endpoint != "" {
			wgArgs = append(wgArgs, "endpoint", p.Endpoint)
		}
		if resp := runTool("wg", wgArgs); resp.ExitCode != 0 {
			return resp
		}
		if err := conf.AddPeer(p); err != nil {
			return failure(fmt.Errorf("peer applied to %s but not persisted: %w", wgInterface, err))
		}
		return privhelper.Response{Output: "peer added\n"}
	}
}

// deploy stores or clears a deployment's environment and token (arguments
// already validated).
func deploy(args []string, input []byte) privhelper.Response {
	op, instance := args[0], args[1]
	if err := privhelper.CheckDeployInput(op, input); err != nil {
		return refused(err)
	}
	var err error
	switch op {
	case "set-env":
		err = deploysecrets.Write(deploysecrets.Dir, instance, deploysecrets.Env, input)
	case "set-token":
		err = deploysecrets.Write(deploysecrets.Dir, instance, deploysecrets.Token, input)
	default: // clear
		err = deploysecrets.Clear(deploysecrets.Dir, instance)
	}
	if err != nil {
		return failure(err)
	}
	return privhelper.Response{Output: op + " " + instance + "\n"}
}

// unitEnv stores or clears namespace units' env files (arguments already
// validated), owned root:orama.
func unitEnv(args []string, input []byte) privhelper.Response {
	if args[0] == "clear" {
		if err := unitenv.ClearNamespace(unitenv.Dir, args[1]); err != nil {
			return failure(err)
		}
		return privhelper.Response{Output: "cleared " + args[1] + "\n"}
	}
	gid, err := oramaGID()
	if err != nil {
		return failure(err)
	}
	if err := unitenv.Write(unitenv.Dir, args[1], args[2], input, unitenv.Owner{UID: 0, GID: gid}); err != nil {
		return failure(err)
	}
	return privhelper.Response{Output: "stored " + args[1] + "/" + args[2] + "\n"}
}

// oramaGID is the orama group, which reads the unit env files.
func oramaGID() (int, error) {
	g, err := user.LookupGroup(allowedUser)
	if err != nil {
		return 0, fmt.Errorf("look up the %s group: %w", allowedUser, err)
	}
	return strconv.Atoi(g.Gid)
}

// notThisNode refuses a peer that claims this node's own mesh address: wg0
// would route this node's overlay traffic for that address to the peer.
func notThisNode(allowedIP string) error {
	own, err := wireguard.GetIP()
	if err != nil {
		return fmt.Errorf("read this node's mesh address: %w", err)
	}
	if allowedIP == own+"/32" {
		return fmt.Errorf("allowed IP %s is this node's own mesh address", allowedIP)
	}
	return nil
}

// removePeer removes every peer holding exactly allowedIP — from the live
// interface (found in `wg show dump`, whose allowed-ips column is a comma list)
// and from the conf.
func removePeer(conf *wireguard.Conf, allowedIP string) privhelper.Response {
	dump := runTool("wg", []string{"show", wgInterface, "dump"})
	if dump.ExitCode != 0 {
		return dump
	}
	removedLive := 0
	for i, line := range strings.Split(strings.TrimSpace(dump.Output), "\n") {
		fields := strings.Split(line, "\t")
		if i == 0 || len(fields) < 4 {
			continue // the interface line
		}
		for _, cidr := range strings.Split(fields[3], ",") {
			if strings.TrimSpace(cidr) == allowedIP {
				if resp := runTool("wg", []string{"set", wgInterface, "peer", fields[0], "remove"}); resp.ExitCode != 0 {
					return resp
				}
				removedLive++
				break
			}
		}
	}
	removedConf, err := conf.RemovePeersByAllowedIP(allowedIP)
	if err != nil {
		return failure(err)
	}
	return privhelper.Response{Output: fmt.Sprintf("removed %d live and %d persisted peers for %s\n", removedLive, removedConf, allowedIP)}
}

// runTool runs a tool from its fixed path with a fixed environment.
func runTool(tool string, args []string) privhelper.Response {
	bin := ""
	for _, p := range toolPaths[tool] {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			bin = p
			break
		}
	}
	if bin == "" {
		return failure(fmt.Errorf("%s not found in %v", tool, toolPaths[tool]))
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	out := &cappedBuffer{limit: maxToolOutput}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return privhelper.Response{Output: out.String()}
	case errors.As(err, &exitErr):
		return privhelper.Response{ExitCode: exitErr.ExitCode(), Output: out.String()}
	default:
		return failure(fmt.Errorf("run %s: %w", tool, err))
	}
}

func failure(err error) privhelper.Response {
	return privhelper.Response{ExitCode: 1, Output: "orama-privhelper: " + err.Error() + "\n"}
}

func refused(err error) privhelper.Response {
	return privhelper.Response{ExitCode: privhelper.ExitRefused, Output: "orama-privhelper: refused: " + err.Error() + "\n"}
}
