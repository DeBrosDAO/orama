package clean

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// Flags holds clean command flags.
type Flags struct {
	Env     string // Target environment
	Node    string // Single node IP
	Nuclear bool   // Also remove shared binaries
	Force   bool   // Skip confirmation
}

// Handle is the entry point for the clean command.
func Handle(args []string) {
	flags, err := parseFlags(args)
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := execute(flags); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}
	fs.StringVar(&flags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	fs.StringVar(&flags.Node, "node", "", "Clean a single node IP only")
	fs.BoolVar(&flags.Nuclear, "nuclear", false, "Also remove shared binaries (rqlited, ipfs, caddy, etc.)")
	fs.BoolVar(&flags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if flags.Env == "" {
		return nil, fmt.Errorf("--env is required\nUsage: orama node clean --env <devnet|testnet> --force")
	}

	return flags, nil
}

func execute(flags *Flags) error {
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
			return fmt.Errorf("node %s not found in %s environment", flags.Node, flags.Env)
		}
	}

	fmt.Printf("Clean %s: %d node(s)\n", flags.Env, len(nodes))
	if flags.Nuclear {
		fmt.Printf("  Mode: NUCLEAR (removes binaries too)\n")
	}
	for _, n := range nodes {
		fmt.Printf("  - %s (%s)\n", n.Host, n.Role)
	}
	fmt.Println()

	// Confirm unless --force
	if !flags.Force {
		fmt.Printf("This will DESTROY all data on these nodes. Anyone relay keys are preserved.\n")
		fmt.Printf("Type 'yes' to confirm: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		if strings.TrimSpace(input) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	// Clean each node
	var failed []string
	for i, node := range nodes {
		fmt.Printf("[%d/%d] Cleaning %s...\n", i+1, len(nodes), node.Host)
		if err := cleanNode(node, flags.Nuclear); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", node.Host, err)
			failed = append(failed, node.Host)
			continue
		}
		fmt.Printf("  ✓ %s cleaned\n\n", node.Host)
	}

	if len(failed) > 0 {
		return fmt.Errorf("clean failed on %d node(s): %s", len(failed), strings.Join(failed, ", "))
	}

	fmt.Printf("✓ Clean complete (%d nodes)\n", len(nodes))
	fmt.Printf("  Anyone relay keys preserved at /var/lib/anon/\n")
	fmt.Printf("  To reinstall: orama node install --vps-ip <ip> ...\n")
	return nil
}

func cleanNode(node inspector.Node, nuclear bool) error {
	sudo := remotessh.SudoPrefix(node)

	nuclearFlag := ""
	if nuclear {
		nuclearFlag = "NUCLEAR=1"
	}

	// The cleanup script runs on the remote node
	script := fmt.Sprintf(`%sbash -c '
%s

# Stop services
for svc in caddy coredns orama-node orama-gateway orama-ipfs-cluster orama-ipfs orama-olric orama-vault orama-anyone-relay orama-anyone-client; do
    systemctl stop "$svc" 2>/dev/null
    systemctl disable "$svc" 2>/dev/null
done

# Kill stragglers
pkill -9 -f "orama-node" 2>/dev/null || true
pkill -9 -f "olric-server" 2>/dev/null || true
pkill -9 -f "ipfs" 2>/dev/null || true

# Remove systemd units
rm -f /etc/systemd/system/orama-*.service
rm -f /etc/systemd/system/coredns.service
rm -f /etc/systemd/system/caddy.service
systemctl daemon-reload 2>/dev/null

# Tear down WireGuard
ip link delete wg0 2>/dev/null || true
rm -f /etc/wireguard/wg0.conf

# Reset firewall
ufw --force reset 2>/dev/null || true
ufw default deny incoming 2>/dev/null || true
ufw default allow outgoing 2>/dev/null || true
ufw allow 22/tcp 2>/dev/null || true
ufw --force enable 2>/dev/null || true

# Remove data
rm -rf /opt/orama

# Clean configs
rm -rf /etc/coredns
rm -rf /etc/caddy
rm -f /tmp/orama-*.sh /tmp/network-source.tar.gz /tmp/orama-*.tar.gz

# Nuclear: remove binaries
if [ -n "$NUCLEAR" ]; then
    rm -f /usr/local/bin/orama /usr/local/bin/orama-node /usr/local/bin/gateway
    rm -f /usr/local/bin/identity /usr/local/bin/sfu /usr/local/bin/turn
    rm -f /usr/local/bin/olric-server /usr/local/bin/ipfs /usr/local/bin/ipfs-cluster-service
    rm -f /usr/local/bin/rqlited /usr/local/bin/coredns
    rm -f /usr/bin/caddy
fi

# Verify Anyone keys preserved
if [ -d /var/lib/anon ]; then
    echo "  Anyone relay keys preserved at /var/lib/anon/"
fi

echo "  Node cleaned successfully"
'`, sudo, nuclearFlag)

	return remotessh.RunSSHStreaming(node, script)
}
