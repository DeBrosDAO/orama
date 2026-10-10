// Package chainreach connects the laptop that runs an operator command to the
// chain of the node it manages. A node's chain API listens on its own loopback,
// or in the orama-global namespace, and is never published; the connection is an
// ssh forward from this machine to that address, opened as the node's ssh user
// and closed when the command ends.
package chainreach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/chainread"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/onchain"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

const (
	// unitDir is where the global layer's units are installed.
	unitDir = "/etc/systemd/system"
	// probeYes is a yes of the probe's answer, which says whether the chain unit
	// is installed and whether it runs in the orama-global namespace.
	probeYes = "yes"
	// nodeInfoPath is the SDK's node info, which names the chain.
	nodeInfoPath = "/cosmos/base/tendermint/v1beta1/node_info"
)

// probeCommand prints "<chain> <colocated>", each yes or no.
var probeCommand = fmt.Sprintf(`c=no; n=no; [ -e %[1]s/%[2]s ] && c=yes; [ -e %[1]s/%[3]s ] && n=yes; echo "$c $n"`,
	unitDir, constants.ChainServiceUnit, globalnetns.UnitName)

// Probe says what a node runs of the chain.
type Probe struct {
	// Chain is true when the node has the chain's unit.
	Chain bool
	// Colocated is true when the chain listens in the orama-global namespace.
	Colocated bool
}

// parseProbe reads probeCommand's output.
func parseProbe(out string) (Probe, error) {
	fields := strings.Fields(out)
	if len(fields) != 2 || (fields[0] != probeYes && fields[0] != "no") || (fields[1] != probeYes && fields[1] != "no") {
		return Probe{}, fmt.Errorf("unexpected answer %q to the chain probe", strings.TrimSpace(out))
	}
	return Probe{Chain: fields[0] == probeYes, Colocated: fields[1] == probeYes}, nil
}

// RESTAddr is the chain REST API as the node itself reaches it.
func (p Probe) RESTAddr() string {
	return fmt.Sprintf("%s:%d", globalnetns.ChainHost(p.Colocated), constants.ChainAPIPort)
}

// RPCAddr is the chain's CometBFT RPC as the node itself reaches it.
func (p Probe) RPCAddr() string {
	return fmt.Sprintf("%s:%d", globalnetns.ChainHost(p.Colocated), constants.ChainRPCPort)
}

// Runner reaches nodes; a test replaces it.
type Runner struct {
	// Output runs a command on a node and returns its stdout.
	Output func(node inspector.Node, command string) (string, error)
	// Tunnel forwards a local port to remote on node and returns its local
	// address and a function that closes it.
	Tunnel func(ctx context.Context, node inspector.Node, remote string) (addr string, closeFn func() error, err error)
}

// SSH is the Runner over the real ssh.
func SSH() Runner {
	return Runner{
		Output: func(node inspector.Node, command string) (string, error) {
			return remotessh.RunSSHOutput(node, command)
		},
		Tunnel: func(ctx context.Context, node inspector.Node, remote string) (string, func() error, error) {
			t, err := remotessh.OpenTunnel(ctx, node, remote)
			if err != nil {
				return "", nil, err
			}
			return t.Addr, t.Close, nil
		},
	}
}

// Reach is an open connection to a chain node's REST API and RPC.
type Reach struct {
	// Node is the node whose chain is reached.
	Node inspector.Node
	// Base is the REST API's URL on this machine, RPCBase the CometBFT RPC's.
	Base, RPCBase string

	closers []func() error
}

// Close closes the forwards.
func (r *Reach) Close() error {
	var errs []error
	for _, c := range r.closers {
		errs = append(errs, c())
	}
	return errors.Join(errs...)
}

// NodeHasChain reports what node runs of the chain.
func (r Runner) NodeHasChain(node inspector.Node) (Probe, error) {
	out, err := r.Output(node, probeCommand)
	if err != nil {
		return Probe{}, fmt.Errorf("check whether %s runs the chain: %w", node.Host, err)
	}
	return parseProbe(out)
}

// Open forwards to the chain REST API of the first candidate that runs the
// chain. Candidates that cannot be asked are skipped, with the reason kept for
// the error when none answers.
func (r Runner) Open(ctx context.Context, candidates []inspector.Node) (*Reach, error) {
	var skipped []error
	for _, node := range candidates {
		probe, err := r.NodeHasChain(node)
		if err != nil {
			skipped = append(skipped, err)
			continue
		}
		if !probe.Chain {
			continue
		}
		reach, err := r.tunnels(ctx, node, probe)
		if err != nil {
			skipped = append(skipped, fmt.Errorf("reach the chain of %s: %w", node.Host, err))
			continue
		}
		return reach, nil
	}
	err := errors.New("none of the nodes runs the chain, or none could be reached")
	if len(skipped) > 0 {
		err = fmt.Errorf("no node's chain could be reached: %w", errors.Join(skipped...))
	}
	return nil, err
}

// tunnels forwards the node's REST API and RPC; both or neither.
func (r Runner) tunnels(ctx context.Context, node inspector.Node, probe Probe) (*Reach, error) {
	restAddr, closeREST, err := r.Tunnel(ctx, node, probe.RESTAddr())
	if err != nil {
		return nil, err
	}
	rpcAddr, closeRPC, err := r.Tunnel(ctx, node, probe.RPCAddr())
	if err != nil {
		return nil, errors.Join(err, closeREST())
	}
	return &Reach{Node: node, Base: "http://" + restAddr, RPCBase: "http://" + rpcAddr, closers: []func() error{closeRPC, closeREST}}, nil
}

// ChainID reads the chain's id from the node.
func (r *Reach) ChainID(ctx context.Context) (string, error) {
	raw, err := (&chainread.Reader{REST: r.Base}).RESTGet(ctx, nodeInfoPath)
	if err != nil {
		return "", fmt.Errorf("read the chain id from %s: %w", r.Node.Host, err)
	}
	var info struct {
		Node struct {
			Network string `json:"network"`
		} `json:"default_node_info"`
	}
	if err := json.Unmarshal(raw, &info); err != nil || info.Node.Network == "" {
		return "", fmt.Errorf("%s answered a node info without a chain id", r.Node.Host)
	}
	return info.Node.Network, nil
}

// CheckChain refuses a node that is not on the chain id want. The node is the one
// the operator manages, but its answer is only a claim: the wallet signs for the chain
// the network is known to run, never for the one the node says it is.
func (r *Reach) CheckChain(ctx context.Context, want string) error {
	if want == "" {
		return errors.New("no chain id to sign for: pass --chain-id <id>")
	}
	id, err := r.ChainID(ctx)
	if err != nil {
		return err
	}
	if id != want {
		return fmt.Errorf("%s runs the chain %q, not the %q this network runs: refusing to sign for it", r.Node.Host, httputil.Printable(id), want)
	}
	return nil
}

// Client is the operator's transaction client for the chain want, signing with
// signer (the RootWallet). The node must be on that chain (CheckChain). The gas,
// fee, account number and sequence of every transaction are derived from the chain.
func (r *Reach) Client(ctx context.Context, signer onchain.Signer, want string) (*onchain.Client, error) {
	if err := r.CheckChain(ctx, want); err != nil {
		return nil, err
	}
	return onchain.New(onchain.REST{Base: r.Base}, signer, want)
}
