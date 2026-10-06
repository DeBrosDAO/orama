package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/client/node"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// defaultRPC is oramad's CometBFT RPC on a co-located machine: it listens on the orama-global
// namespace address (core/pkg/constants, GlobalNetnsAddr), which the host reaches over the veth pair.
const defaultRPC = "tcp://198.18.0.2:31001"

func run(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: stagenet-node agent|address|epoch|earnings|fee-balance|tx-fee|register-operator|fund-hot-key|node-status [flags]")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "agent":
		return cmdAgent(ctx, rest, stdin)
	case "address":
		return cmdAddress(rest, out)
	case "epoch":
		return cmdEpoch(ctx, rest, out)
	case "earnings":
		return cmdEarnings(ctx, rest, out)
	case "fee-balance":
		return cmdFeeBalance(ctx, rest, out)
	case "tx-fee":
		return cmdTxFee(ctx, rest, out)
	case "register-operator":
		return cmdRegisterOperator(ctx, rest, stdin, out)
	case "fund-hot-key":
		return cmdFundHotKey(ctx, rest, stdin, out)
	case "node-status":
		return cmdNodeStatus(ctx, rest, out)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

type listenFlags []listenSpec

func (l *listenFlags) String() string { return fmt.Sprint(*l) }

func (l *listenFlags) Set(v string) error {
	spec, err := parseListen(v)
	if err != nil {
		return err
	}
	*l = append(*l, spec)
	return nil
}

func cmdAgent(ctx context.Context, args []string, stdin io.Reader) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	var listens listenFlags
	fs.Var(&listens, "listen", "socket to serve, as path:uid (repeatable)")
	ttl := fs.Duration("ttl", 0, "stop after this long, so an agent whose caller vanished does not keep the key in memory (0 is no limit)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ttl < 0 {
		return errors.New("--ttl must not be negative")
	}
	acct, err := accountFromStdin(stdin)
	if err != nil {
		return err
	}
	if *ttl > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *ttl)
		defer cancel()
	}
	return serveAgent(ctx, newAgentHandler(acct), listens)
}

func cmdAddress(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("address", flag.ContinueOnError)
	keyFile := fs.String("key-file", "", "hex secp256k1 key file (a provider's hot-key)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyFile == "" {
		return errors.New("--key-file is required")
	}
	addr, err := addressOfKeyFile(*keyFile)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, addr)
	return nil
}

func dial(rpc string) (*node.Client, error) {
	return node.DialWith(rpc, nodestypes.RegisterInterfaces)
}

func cmdEpoch(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("epoch", flag.ContinueOnError)
	rpc := fs.String("rpc", defaultRPC, "oramad CometBFT RPC")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := dial(*rpc)
	if err != nil {
		return err
	}
	var resp emissiontypes.QueryCurrentEpochResponse
	if err := c.Query(ctx, "/orama.emission.v1.Query/CurrentEpoch", &emissiontypes.QueryCurrentEpochRequest{}, &resp); err != nil {
		return fmt.Errorf("read the current epoch from %s (is the chain running?): %w", *rpc, err)
	}
	fmt.Fprintln(out, resp.EpochState.CurrentEpoch)
	return nil
}

func cmdEarnings(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("earnings", flag.ContinueOnError)
	rpc := fs.String("rpc", defaultRPC, "oramad CometBFT RPC")
	addr := fs.String("address", "", "account address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *addr == "" {
		return errors.New("--address is required")
	}
	c, err := dial(*rpc)
	if err != nil {
		return err
	}
	var resp feestypes.QueryEarningsResponse
	if err := c.Query(ctx, "/orama.fees.v1.Query/Earnings", &feestypes.QueryEarningsRequest{Address: *addr}, &resp); err != nil {
		return fmt.Errorf("read the earnings of %s: %w", *addr, err)
	}
	fmt.Fprintln(out, resp.Balance.String())
	return nil
}

func cmdFeeBalance(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("fee-balance", flag.ContinueOnError)
	rpc := fs.String("rpc", defaultRPC, "oramad CometBFT RPC")
	addr := fs.String("address", "", "account address (a node's hot key)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *addr == "" {
		return errors.New("--address is required")
	}
	c, err := dial(*rpc)
	if err != nil {
		return err
	}
	var resp feestypes.QueryFeeBalanceResponse
	if err := c.Query(ctx, "/orama.fees.v1.Query/FeeBalance", &feestypes.QueryFeeBalanceRequest{Address: *addr}, &resp); err != nil {
		return fmt.Errorf("read the fee balance of %s: %w", *addr, err)
	}
	fmt.Fprintln(out, resp.Balance.String())
	return nil
}

func cmdRegisterOperator(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("register-operator", flag.ContinueOnError)
	rpc := fs.String("rpc", defaultRPC, "oramad CometBFT RPC")
	if err := fs.Parse(args); err != nil {
		return err
	}
	acct, err := accountFromStdin(stdin)
	if err != nil {
		return err
	}
	c, err := dial(*rpc)
	if err != nil {
		return err
	}
	var have nodestypes.QueryOperatorResponse
	err = c.Query(ctx, "/orama.nodes.v1.Query/Operator", &nodestypes.QueryOperatorRequest{Address: acct.Address}, &have)
	var qerr *node.QueryError
	switch {
	case err == nil:
		fmt.Fprintf(out, "operator %s is already registered\n", acct.Address)
		return nil
	case !errors.As(err, &qerr) || !qerr.NotFound():
		return fmt.Errorf("look up operator %s: %w", acct.Address, err)
	}
	msg := &nodestypes.MsgRegisterOperator{Operator: acct.Address}
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	hash, err := c.Submit(ctx, acct, msg)
	if err != nil {
		return fmt.Errorf("register operator %s: %w", acct.Address, err)
	}
	fmt.Fprintf(out, "registered operator %s: %s\n", acct.Address, hash)
	return nil
}

func cmdFundHotKey(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("fund-hot-key", flag.ContinueOnError)
	rpc := fs.String("rpc", defaultRPC, "oramad CometBFT RPC")
	nodeID := fs.String("node-id", "", "x/nodes node id")
	amount := fs.String("amount", "", "amount of norama")
	if err := fs.Parse(args); err != nil {
		return err
	}
	amt, ok := math.NewIntFromString(*amount)
	if !ok {
		return fmt.Errorf("--amount %q is not an integer number of norama", *amount)
	}
	acct, err := accountFromStdin(stdin)
	if err != nil {
		return err
	}
	c, err := dial(*rpc)
	if err != nil {
		return err
	}
	msg := &nodestypes.MsgFundHotKey{Operator: acct.Address, NodeId: *nodeID, Amount: amt}
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	hash, err := c.Submit(ctx, acct, msg)
	if err != nil {
		return fmt.Errorf("fund the hot key of %s: %w", *nodeID, err)
	}
	fmt.Fprintf(out, "funded the hot key of %s: %s\n", *nodeID, hash)
	return nil
}

func cmdNodeStatus(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("node-status", flag.ContinueOnError)
	rpc := fs.String("rpc", defaultRPC, "oramad CometBFT RPC")
	id := fs.String("id", "", "x/nodes node id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("--id is required")
	}
	c, err := dial(*rpc)
	if err != nil {
		return err
	}
	var resp nodestypes.QueryNodeResponse
	err = c.Query(ctx, "/orama.nodes.v1.Query/Node", &nodestypes.QueryNodeRequest{NodeId: *id}, &resp)
	var qerr *node.QueryError
	if errors.As(err, &qerr) && qerr.NotFound() {
		fmt.Fprint(out, formatNodeStatus(nil))
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up node %s: %w", *id, err)
	}
	fmt.Fprint(out, formatNodeStatus(&resp.Node))
	return nil
}

// formatNodeStatus is the key=value view deploy.sh reads: exists, then the fields its idempotent
// steps decide on. A nil node is "exists=false".
func formatNodeStatus(n *nodestypes.Node) string {
	if n == nil {
		return "exists=false\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "exists=true\nstatus=%s\noperator=%s\nhot_key=%s\nasn=%d\ncapacity=%d\n",
		n.Status, n.Operator, n.HotKey, n.Asn, n.DeclaredCapacityBytes)
	bond := func(role nodestypes.Role) string {
		for _, rb := range n.Bonds {
			if rb.Role == role {
				return rb.Amount.String()
			}
		}
		return "0"
	}
	fmt.Fprintf(&b, "storage_bond=%s\narchiver_bond=%s\n", bond(nodestypes.RoleStorage), bond(nodestypes.RoleArchiver))
	return b.String()
}
