package decommission

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
)

// WipeFlags holds wipe command flags.
type WipeFlags struct {
	Env     string
	Node    string
	Nuclear bool
	Force   bool
}

// HandleWipe is the entry point for `orama node wipe`.
//
// Target-side only: it erases a node and says nothing to the cluster. Use it
// when the node is already retired (a `decommission --offline` was run, or it
// never joined), or to finish a decommission whose wipe failed.
func RunWipe(flags *WipeFlags) error {
	if err := flags.validate(); err != nil {
		return err
	}
	return executeWipe(flags)
}

func (f *WipeFlags) validate() error {
	if f.Env == "" {
		return fmt.Errorf("--env is required\nUsage: orama node wipe --env <devnet|testnet> [--node <ip>] --force")
	}
	return nil
}

func executeWipe(flags *WipeFlags) error {
	nodes, err := remotessh.LoadEnvNodes(flags.Env)
	if err != nil {
		return err
	}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return err
	}
	defer cleanup()

	if flags.Node != "" {
		nodes = remotessh.FilterByIP(nodes, flags.Node)
		if len(nodes) == 0 {
			return fmt.Errorf("node %s not found in the %s environment", flags.Node, flags.Env)
		}
	}

	fmt.Printf("Wipe %s: %d node(s)\n", flags.Env, len(nodes))
	if flags.Nuclear {
		fmt.Printf("  Mode: NUCLEAR (removes shared binaries too)\n")
	}
	for _, n := range nodes {
		fmt.Printf("  - %s (%s)\n", n.Host, n.Role)
	}
	fmt.Println()

	if flags.Node != "" && len(nodes) == 1 {
		fmt.Printf("Note: this erases the node but tells the cluster nothing. If it is still a\n")
		fmt.Printf("      member, use `orama node decommission` instead, or the survivors will keep\n")
		fmt.Printf("      counting it toward quorum.\n\n")
	}

	if !flags.Force {
		fmt.Printf("This will DESTROY all data on these nodes. Anyone relay keys are preserved.\n")
		fmt.Printf("Type 'yes' to confirm: ")
		input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(input) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	var failed []string
	for i, node := range nodes {
		fmt.Printf("[%d/%d] Wiping %s...\n", i+1, len(nodes), node.Host)
		if err := wipeNode(node, flags.Nuclear); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", node.Host, err)
			failed = append(failed, node.Host)
			continue
		}
		fmt.Printf("  ✓ %s wiped\n\n", node.Host)
	}

	if len(failed) > 0 {
		return fmt.Errorf("wipe failed on %d node(s): %s", len(failed), strings.Join(failed, ", "))
	}

	fmt.Printf("✓ Wipe complete (%d nodes)\n", len(nodes))
	fmt.Printf("  Anyone relay keys preserved at /var/lib/anon/ (DESTROY_ANON=1 to remove)\n")
	fmt.Printf("  rm -rf is unlink, not cryptographic erase. Provider disks remain readable.\n")
	fmt.Printf("  To reinstall: orama node install --vps-ip <ip> ...\n")
	return nil
}
