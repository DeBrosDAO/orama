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
	DryRun  bool
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

// resolveRaftID finds the target's id in the platform raft configuration.
//
// Members are keyed by id, which on a cluster that has run
// `orama node migrate-raft-id` is a peer id and before it is the raft address.
// Removing by address matched nothing on a migrated cluster and reported
// success, leaving the retired machine a configured voter for ever.
func resolveRaftID(survivor inspector.Node, raftAddr string) (string, error) {
	members, err := clusterops.RaftMembers(survivor)
	if err != nil {
		return "", err
	}
	if id := clusterops.IDForAddr(members, raftAddr); id != "" {
		return id, nil
	}
	return raftAddr, nil
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

	record, err := clusterops.ResolveNodeRecord(survivor, target.Host)
	if err != nil {
		return err
	}
	fmt.Printf("  Node record: peer %s, overlay %s\n", record.PeerID, record.InternalIP)

	raftAddr := fmt.Sprintf("%s:%d", record.InternalIP, constants.RQLiteRaftPort)
	raftNodeID, err := resolveRaftID(survivor, raftAddr)
	if err != nil {
		return err
	}

	// The quorum arithmetic comes before the confirmation prompt, and covers
	// every namespace this node holds a voter in as well as the platform
	// cluster. Checking only the platform cluster, at the point of the raft
	// removal, is how an operator retired a node that held two of three voters
	// for a namespace and learned about it when that namespace stopped
	// accepting writes.
	impacts, err := clusterops.PlanRemoval(survivor, raftNodeID, record.PeerID)
	if err != nil {
		return err
	}
	fmt.Printf("\n  Quorum after removing %s:\n%s", target.Host, clusterops.FormatImpacts(impacts))

	if !clusterops.Safe(impacts) {
		return fmt.Errorf("refusing to remove %s: it would cost a cluster its quorum (see above)", target.Host)
	}

	if flags.DryRun {
		fmt.Printf("\n  --dry-run, so nothing was changed. This would run:\n")
		for _, step := range clusterops.RetirementPlan(record) {
			fmt.Printf("    %s\n      %s\n", step.What, step.SQL)
		}
		fmt.Printf("    remove raft member %s\n", raftNodeID)
		if !flags.Offline {
			fmt.Printf("    wipe %s\n", target.Host)
		}
		return nil
	}

	if !flags.Force {
		fmt.Printf("\nThis removes %s from raft, the mesh and every namespace it serves", target.Host)
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

	if err := clusterops.RemoveRaftMember(survivor, raftNodeID); err != nil {
		return err
	}

	if err := clusterops.WriteTombstone(survivor, raftNodeID, raftAddr, record.PeerID, survivor.Host); err != nil {
		return err
	}
	fmt.Printf("  ✓ tombstoned, so nothing re-adds it automatically\n")

	if err := clusterops.Retire(survivor, record); err != nil {
		return err
	}

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
