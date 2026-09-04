package push

import (
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/build"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// Flags holds push command flags.
type Flags struct {
	Env    string // Target environment; empty means the active one
	Node   string // Restrict to a single node IP from the inventory
	Host   string // Push to a node that is not in the inventory
	User   string // SSH user for Host (default root)
	Direct bool   // Upload from here to each node in turn, instead of fanning out
}

// Run is the entry point for the push command.
func Run(flags *Flags) error {
	if err := flags.validate(); err != nil {
		return err
	}
	return execute(flags)
}

func (f *Flags) validate() error {
	if f.Env == "" && f.Host == "" {
		return fmt.Errorf("specify --env <devnet|testnet> or --host <ip>")
	}
	return nil
}

// resolveTargets returns the nodes to push to.
//
// A --host names a machine that is not in the inventory yet, which is how a
// node is seeded before it can be resolved. Otherwise the environment's nodes
// come from the resolver, so this command sees the same fleet as every other.
func resolveTargets(flags *Flags) ([]inspector.Node, error) {
	env := flags.Env
	if env == "" {
		active, err := cli.GetActiveEnvironment()
		if err != nil {
			return nil, fmt.Errorf("no --env given and no active environment: %w", err)
		}
		env = active.Name
	}

	if flags.Host != "" {
		return []inspector.Node{noderesolver.NewNode(flags.Host, flags.User, env)}, nil
	}

	nodes, err := noderesolver.ResolveNodes(env)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes found for environment %q", env)
	}

	if flags.Node != "" {
		nodes = remotessh.FilterByIP(nodes, flags.Node)
		if len(nodes) == 0 {
			return nil, fmt.Errorf("node %s not found in %s environment", flags.Node, env)
		}
	}
	return nodes, nil
}

func execute(flags *Flags) error {
	// Find archive
	archivePath := build.FindNewestArchive()
	if archivePath == "" {
		return fmt.Errorf("no binary archive found in /tmp/ (run `orama build` first)")
	}

	info, _ := os.Stat(archivePath)
	fmt.Printf("Archive: %s (%s)\n", filepath.Base(archivePath), printer.FormatBytes(info.Size()))

	nodes, err := resolveTargets(flags)
	if err != nil {
		return err
	}

	// Prepare wallet-derived SSH keys
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Printf("Targets: %d node(s)\n\n", len(nodes))

	if flags.Direct || len(nodes) == 1 {
		return pushDirect(archivePath, nodes)
	}

	return pushFanout(archivePath, nodes)
}

// pushDirect uploads the archive to each node sequentially.
func pushDirect(archivePath string, nodes []inspector.Node) error {
	remotePath := "/tmp/" + filepath.Base(archivePath)

	for i, node := range nodes {
		fmt.Printf("[%d/%d] Pushing to %s...\n", i+1, len(nodes), node.Host)

		if err := remotessh.UploadFile(node, archivePath, remotePath); err != nil {
			return fmt.Errorf("upload to %s failed: %w", node.Host, err)
		}

		if err := extractOnNode(node, remotePath, false); err != nil {
			return fmt.Errorf("extract on %s failed: %w", node.Host, err)
		}

		fmt.Printf("  ✓ %s done\n\n", node.Host)
	}

	fmt.Printf("✓ Push complete (%d nodes)\n", len(nodes))
	return nil
}

// pushFanout uploads to a hub node, then fans out to all others via agent forwarding.
func pushFanout(archivePath string, nodes []inspector.Node) error {
	hub := remotessh.PickHubNode(nodes)
	remotePath := "/tmp/" + filepath.Base(archivePath)

	// Step 1: Upload to hub
	fmt.Printf("[hub] Uploading to %s...\n", hub.Host)
	if err := remotessh.UploadFile(hub, archivePath, remotePath); err != nil {
		return fmt.Errorf("upload to hub %s failed: %w", hub.Host, err)
	}

	// Keep the archive on the hub — the fanout below scp's it to every other
	// node. (Removing it here was the bug that broke fanout: the subsequent scp
	// from the hub found no local file.)
	if err := extractOnNode(hub, remotePath, true); err != nil {
		return fmt.Errorf("extract on hub %s failed: %w", hub.Host, err)
	}
	fmt.Printf("  ✓ hub %s done\n\n", hub.Host)

	// Step 2: Fan out from hub to remaining nodes in parallel (via agent forwarding)
	remaining := make([]inspector.Node, 0, len(nodes)-1)
	for _, n := range nodes {
		if n.Host != hub.Host {
			remaining = append(remaining, n)
		}
	}

	if len(remaining) == 0 {
		fmt.Printf("✓ Push complete (1 node)\n")
		return nil
	}

	// Stage each target's SSH key on the hub so the hub authenticates to the
	// target with a SINGLE key (-i + IdentitiesOnly), instead of agent-forwarding
	// ALL node keys — which the target's sshd rejects with "too many
	// authentication failures" once the offered-key count exceeds MaxAuthTries
	// (default 6). Keys are chmod 600 and removed (defer) when the fanout ends.
	const fanoutKeyDir = "/dev/shm/.orama-fanout-keys"
	if err := remotessh.RunSSHStreaming(hub, "rm -rf "+fanoutKeyDir+" && mkdir -p "+fanoutKeyDir+" && chmod 700 "+fanoutKeyDir); err != nil {
		return fmt.Errorf("prepare fanout key dir on hub: %w", err)
	}
	defer func() {
		wipe := "for f in " + fanoutKeyDir + "/*; do [ -f \"$f\" ] && dd if=/dev/zero of=\"$f\" bs=8192 count=1 conv=notrunc status=none 2>/dev/null; done; rm -rf " + fanoutKeyDir
		if err := remotessh.RunSSHStreaming(hub, wipe); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to wipe fanout keys on hub: %v\n", err)
		}
	}()
	for _, t := range remaining {
		dst := fanoutKeyDir + "/" + t.Host
		if err := remotessh.UploadFile(hub, t.SSHKey, dst); err != nil {
			return fmt.Errorf("stage key for %s on hub: %w", t.Host, err)
		}
		if err := remotessh.RunSSHStreaming(hub, "chmod 600 "+dst); err != nil {
			return fmt.Errorf("chmod staged key for %s: %w", t.Host, err)
		}
	}

	fmt.Printf("[fanout] Distributing from %s to %d nodes...\n", hub.Host, len(remaining))

	var wg sync.WaitGroup
	errors := make([]error, len(remaining))

	for i, target := range remaining {
		wg.Add(1)
		go func(idx int, target inspector.Node) {
			defer wg.Done()

			// SCP from hub to target using the target's staged key only.
			keyPath := fanoutKeyDir + "/" + target.Host
			scpCmd := fmt.Sprintf("scp -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes -i %s -o ConnectTimeout=10 %s %s@%s:%s",
				keyPath, remotePath, target.User, target.Host, remotePath)

			if err := remotessh.RunSSHStreaming(hub, scpCmd); err != nil {
				errors[idx] = fmt.Errorf("fanout to %s failed: %w", target.Host, err)
				return
			}

			if err := extractOnNodeVia(hub, target, remotePath, keyPath); err != nil {
				errors[idx] = fmt.Errorf("extract on %s failed: %w", target.Host, err)
				return
			}

			fmt.Printf("  ✓ %s done\n", target.Host)
		}(i, target)
	}

	wg.Wait()

	// Fanout done — remove the hub's retained archive (best-effort).
	_ = remotessh.RunSSHStreaming(hub, remotessh.SudoPrefix(hub)+"rm -f "+remotePath)

	// Check for errors
	var failed []string
	for i, err := range errors {
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", remaining[i].Host, err)
			failed = append(failed, remaining[i].Host)
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("push failed on %d node(s): %s", len(failed), strings.Join(failed, ", "))
	}

	fmt.Printf("\n✓ Push complete (%d nodes)\n", len(nodes))
	return nil
}

// extractOnNode extracts the archive on a remote node. When keepArchive is true
// the uploaded tarball is left in place — the hub needs it to fan out to the
// other nodes; otherwise it is removed after extraction to reclaim /tmp.
func extractOnNode(node inspector.Node, remotePath string, keepArchive bool) error {
	sudo := remotessh.SudoPrefix(node)
	cmd := fmt.Sprintf("%smkdir -p /opt/orama && %star xzf %s -C /opt/orama",
		sudo, sudo, remotePath)
	if !keepArchive {
		cmd += fmt.Sprintf(" && %srm -f %s", sudo, remotePath)
	}
	return remotessh.RunSSHStreaming(node, cmd)
}

// extractOnNodeVia extracts the archive on a target node by SSHing through the
// hub, authenticating with the target's staged key (keyPath) only — avoiding the
// forwarded-agent "too many authentication failures".
func extractOnNodeVia(hub, target inspector.Node, remotePath, keyPath string) error {
	sudo := remotessh.SudoPrefix(target)
	extractCmd := fmt.Sprintf("%smkdir -p /opt/orama && %star xzf %s -C /opt/orama && %srm -f %s",
		sudo, sudo, remotePath, sudo, remotePath)

	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes -i %s -o ConnectTimeout=10 %s@%s '%s'",
		keyPath, target.User, target.Host, extractCmd)

	return remotessh.RunSSHStreaming(hub, sshCmd)
}
