package decommission

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/production/clusterops"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// Flags holds decommission command flags.
type Flags struct {
	Env     string
	Node    string
	Offline bool
	Nuclear bool
	Force   bool
}

// Run is the entry point for `orama node decommission`.
func Run(flags *Flags) error {
	if err := flags.validate(); err != nil {
		return err
	}
	return execute(flags)
}

func (f *Flags) validate() error {
	if f.Env == "" {
		return fmt.Errorf("--env is required\nUsage: orama node decommission --env <devnet|testnet> --node <ip> [--offline] [--force]")
	}
	if f.Node == "" {
		return fmt.Errorf("--node is required: decommission removes ONE node")
	}
	return nil
}

func execute(flags *Flags) error {
	nodes, err := noderesolver.ResolveNodes(flags.Env)
	if err != nil {
		return err
	}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return err
	}
	defer cleanup()

	targets := remotessh.FilterByIP(nodes, flags.Node)
	if len(targets) == 0 {
		return fmt.Errorf("node %s not found in the %s environment", flags.Node, flags.Env)
	}
	target := targets[0]

	survivor, err := clusterops.PickSurvivor(nodes, target.Host)
	if err != nil {
		return err
	}

	fmt.Printf("Decommission %s from %s\n", target.Host, flags.Env)
	fmt.Printf("  Driving the cluster-side removal from %s\n", survivor.Host)
	if flags.Offline {
		fmt.Printf("  --offline: the node will NOT be wiped\n")
	}
	fmt.Println()

	if !flags.Force {
		fmt.Printf("This removes %s from raft, the mesh and the node registry", target.Host)
		if !flags.Offline {
			fmt.Printf(", then ERASES it")
		}
		fmt.Printf(".\nType 'yes' to confirm: ")
		input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(input) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	record, err := clusterops.ResolveNodeRecord(survivor, target.Host)
	if err != nil {
		return err
	}
	fmt.Printf("  Node record: peer %s, overlay %s\n", record.PeerID, record.InternalIP)

	raftAddr := fmt.Sprintf("%s:%d", record.InternalIP, constants.RQLiteRaftPort)

	// Resolve the raft ID from the configuration rather than assuming it is the
	// address. On a cluster that has run `orama node migrate-raft-id` the member
	// is registered under its peer id, and removing by address matched nothing
	// and reported success — leaving the decommissioned machine a configured
	// voter for ever.
	members, err := clusterops.RaftMembers(survivor)
	if err != nil {
		return err
	}
	raftNodeID := clusterops.IDForAddr(members, raftAddr)
	if raftNodeID == "" {
		raftNodeID = raftAddr
	}

	if err := clusterops.RemoveRaftMember(survivor, raftNodeID); err != nil {
		return err
	}

	if err := clusterops.WriteTombstone(survivor, raftNodeID, raftAddr, record.PeerID, survivor.Host); err != nil {
		return err
	}
	fmt.Printf("  ✓ tombstoned, so nothing re-adds it automatically\n")

	if err := deleteMembershipRows(survivor, record); err != nil {
		return err
	}
	fmt.Printf("  ✓ removed from wireguard_peers and dns_nodes\n")

	if flags.Offline {
		fmt.Printf("\n✓ %s retired cluster-side. It was not wiped (--offline).\n", target.Host)
		return nil
	}

	fmt.Printf("\n  Wiping %s...\n", target.Host)
	if err := wipeNode(target, flags.Nuclear); err != nil {
		return fmt.Errorf("the node was retired cluster-side but the wipe failed: %w\n"+
			"  Re-run `orama node wipe --env %s --node %s` once it is reachable", err, flags.Env, target.Host)
	}

	fmt.Printf("\n✓ %s decommissioned and wiped\n", target.Host)
	fmt.Printf("  rm -rf is unlink, not cryptographic erase. Provider disks remain readable.\n")
	return nil
}

// deleteMembershipRows removes the node from the stores the reconciler would
// otherwise take hours to catch up on.
//
// The reconciler would get there on its own — that is the point of it — but an
// operator who asked for a removal should not have to wait out a liveness grace
// to see it happen.
func deleteMembershipRows(survivor inspector.Node, rec clusterops.NodeRecord) error {
	stmts := []string{
		fmt.Sprintf(`DELETE FROM wireguard_peers WHERE wg_ip = '%s'`, clusterops.SQLLiteral(rec.InternalIP)),
		fmt.Sprintf(`DELETE FROM dns_nodes WHERE id = '%s'`, clusterops.SQLLiteral(rec.PeerID)),
	}
	for _, stmt := range stmts {
		if err := clusterops.ExecSQL(survivor, stmt); err != nil {
			return err
		}
	}
	return nil
}
