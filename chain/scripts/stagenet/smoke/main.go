// Command stagenetctl drives the live checks of the stagenet deploy (chain/scripts/stagenet/deploy.sh
// smoke). It runs on the operator's machine and reaches each node the way an operator does: over
// ssh, into the orama-global network namespace where the chain's RPC and REST API listen on
// loopback, and through the signing agent stagenet-node runs beside the chain, so no key ever
// leaves a node.
//
//	stagenetctl smoke        run the checks and print PASS, FAIL or SKIP for each
//	stagenetctl shielded-env print the wallet builder's environment for `deploy.sh gen-shielded`
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
)

var (
	nodeNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	aliasRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	ipRE       = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3}){3}$`)
	chainIDRE  = regexp.MustCompile(`^[a-z0-9-]{1,48}$`)
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, err := run(ctx, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "stagenetctl:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}

// parseNodes reads "name=alias=ip,name=alias=ip" into node references. Every field is checked: the
// alias goes to ssh and the name into a node id.
func parseNodes(spec string) ([]nodeRef, error) {
	var out []nodeRef
	seen := map[string]bool{}
	for _, item := range strings.Split(spec, ",") {
		f := strings.Split(strings.TrimSpace(item), "=")
		if len(f) != 3 {
			return nil, fmt.Errorf("node %q is not name=alias=ip", item)
		}
		n := nodeRef{Name: f[0], Alias: f[1], IP: f[2]}
		if !nodeNameRE.MatchString(n.Name) || !aliasRE.MatchString(n.Alias) || !ipRE.MatchString(n.IP) {
			return nil, fmt.Errorf("node %q has an invalid name, alias or address", item)
		}
		if seen[n.Name] {
			return nil, fmt.Errorf("node %q is listed twice", n.Name)
		}
		seen[n.Name] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("no nodes")
	}
	return out, nil
}

type commonFlags struct {
	chainID string
	nodes   string
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.chainID, "chain-id", os.Getenv("CHAIN_ID"), "chain id (must contain -stagenet- or -devnet-)")
	fs.StringVar(&c.nodes, "nodes", "", "nodes as name=alias=ip,... [required]")
}

// validate refuses a chain id that is not a stagenet or devnet one, the same guard deploy.sh has.
func (c commonFlags) validate() ([]nodeRef, error) {
	if !chainIDRE.MatchString(c.chainID) {
		return nil, fmt.Errorf("invalid --chain-id %q", c.chainID)
	}
	if !strings.Contains(c.chainID, "-stagenet-") && !strings.Contains(c.chainID, "-devnet-") {
		return nil, fmt.Errorf("refusing to run: the chain id must contain -stagenet- or -devnet-, got %q", c.chainID)
	}
	return parseNodes(c.nodes)
}

func run(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 {
		return 2, errors.New("usage: stagenetctl smoke|shielded-env [flags]")
	}
	switch args[0] {
	case "smoke":
		return runSmoke(ctx, args[1:])
	case "shielded-env":
		return runShieldedEnv(ctx, args[1:])
	default:
		return 2, fmt.Errorf("unknown command %q", args[0])
	}
}
