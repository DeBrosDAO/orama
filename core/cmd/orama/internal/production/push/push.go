package push

import (
	"errors"
	"fmt"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

// Flags holds push command flags.
type Flags struct {
	Env    string // Target environment; empty means the active one
	Node   string // Restrict to a single node IP from the inventory
	Host   string // Push to a node that is not in the inventory
	User   string // SSH user for Host (default root)
	Direct bool   // Upload from here to each node in turn, instead of fanning out
	// Archive is the build to push. Required: the newest archive in /tmp may
	// be another checkout's build.
	Archive string
	// TrustSigners creates the trust anchor on nodes that have none — nodes
	// installed before archives were signed. It never changes an existing one.
	TrustSigners []string
}

// stageHint says what a failed node-side stage most often means.
const stageHint = "without --trust-signers each node verifies the archive with its installed orama (" + NodeOramaBinary +
	" node stage-archive) against /etc/orama/archive-signers. A node on a release from before " +
	"archive signing has no such command and no such file: push to it once with --trust-signers, which " +
	"stages with the archive's own verified CLI (see docs/DEV_DEPLOY.md, \"Signed archives\")"

// errArchiveRequired names the archive to push explicitly: /tmp is shared, and
// "the newest archive there" was a build from another checkout often enough.
var errArchiveRequired = fmt.Errorf("--archive is required: the path `orama build` printed")

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
	if f.Archive == "" {
		return errArchiveRequired
	}
	if len(f.TrustSigners) > 0 {
		signers, err := archivetrust.NormalizeSigners(f.TrustSigners)
		if err != nil {
			return fmt.Errorf("--trust-signers: %w", err)
		}
		f.TrustSigners = signers
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
	archivePath := flags.Archive
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("archive %s: %w", archivePath, err)
	}
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
	return ToNodes(archivePath, nodes, flags.Direct, flags.TrustSigners)
}

// ToNodes pushes the archive at archivePath to nodes, whose SSH keys are
// prepared: uploaded to each node in turn when direct (or there is one node),
// otherwise to a hub that fans it out.
//
// Without trust every node stages it with its installed orama, which verifies
// it against the node's trust anchor. With trust the archive is verified here
// against trust and staged with its own verified CLI, which creates a missing
// anchor from trust and requires an existing one to be exactly trust — the
// push for nodes installed before archive signing, whose CLI cannot stage
// anything (see archive_cli.go).
func ToNodes(archivePath string, nodes []inspector.Node, direct bool, trust []string) (err error) {
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes to push to")
	}
	stager, cleanup, err := newNodeStager(archivePath, trust)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cleanup()) }()
	if direct || len(nodes) == 1 {
		return pushDirect(nodes, stager)
	}
	return pushFanout(nodes, stager)
}

// pushDirect uploads the archive to each node sequentially.
func pushDirect(nodes []inspector.Node, stager *nodeStager) error {
	for i, node := range nodes {
		fmt.Printf("[%d/%d] Pushing to %s...\n", i+1, len(nodes), node.Host)
		dir, err := makeUploadDir(func(cmd string) (string, error) { return remotessh.RunSSHOutput(node, cmd) })
		if err != nil {
			return fmt.Errorf("upload to %s failed: %w", node.Host, err)
		}
		if err := remotessh.UploadFile(node, stager.archive, uploadPath(dir)); err != nil {
			if rmErr := remotessh.RunSSHStreaming(node, remotessh.SudoPrefix(node)+"rm -rf "+dir); rmErr != nil {
				err = errors.Join(err, fmt.Errorf("remove the upload %s on %s: %w", dir, node.Host, rmErr))
			}
			return fmt.Errorf("upload to %s failed: %w", node.Host, err)
		}
		if err := remotessh.RunSSHStreaming(node, stager.stageAndRemove(remotessh.SudoPrefix(node), dir)); err != nil {
			return fmt.Errorf("stage on %s failed: %w\n  %s", node.Host, err, stageHint)
		}
		fmt.Printf("  ✓ %s done\n\n", node.Host)
	}

	fmt.Printf("✓ Push complete (%d nodes)\n", len(nodes))
	return nil
}

// pushFanout uploads to a hub node, then fans out to all others via agent forwarding.
func pushFanout(nodes []inspector.Node, stager *nodeStager) (err error) {
	hub := remotessh.PickHubNode(nodes)

	// Step 1: Upload to the hub and stage there. The upload stays on the hub
	// until the fanout below has copied it to every other node.
	fmt.Printf("[hub] Uploading to %s...\n", hub.Host)
	hubDir, err := makeUploadDir(func(cmd string) (string, error) { return remotessh.RunSSHOutput(hub, cmd) })
	if err != nil {
		return fmt.Errorf("upload to hub %s failed: %w", hub.Host, err)
	}
	defer func() {
		if rmErr := remotessh.RunSSHStreaming(hub, remotessh.SudoPrefix(hub)+"rm -rf "+hubDir); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove the upload %s from hub %s: %w", hubDir, hub.Host, rmErr))
		}
	}()
	if err := remotessh.UploadFile(hub, stager.archive, uploadPath(hubDir)); err != nil {
		return fmt.Errorf("upload to hub %s failed: %w", hub.Host, err)
	}
	if err := remotessh.RunSSHStreaming(hub, stager.stage(remotessh.SudoPrefix(hub), uploadPath(hubDir))); err != nil {
		return fmt.Errorf("stage on hub %s failed: %w\n  %s", hub.Host, err, stageHint)
	}
	fmt.Printf("  ✓ hub %s done\n\n", hub.Host)

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
	return fanOut(hub, uploadPath(hubDir), remaining, stager)
}

// fanOut copies the archive at hubPath from the hub to every target in
// parallel and stages it on each.
func fanOut(hub inspector.Node, hubPath string, remaining []inspector.Node, stager *nodeStager) (err error) {
	// Stage each target's SSH key on the hub so the hub authenticates to the
	// target with a SINGLE key (-i + IdentitiesOnly), instead of agent-forwarding
	// ALL node keys — which the target's sshd rejects with "too many
	// authentication failures" once the offered-key count exceeds MaxAuthTries
	// (default 6). Keys are chmod 600 and wiped when the fanout ends.
	if err := remotessh.RunSSHStreaming(hub, "rm -rf "+fanoutKeyDir+" && mkdir -p "+fanoutKeyDir+" && chmod 700 "+fanoutKeyDir); err != nil {
		return fmt.Errorf("prepare fanout key dir on hub: %w", err)
	}
	defer func() {
		wipe := "for f in " + fanoutKeyDir + "/*; do [ -f \"$f\" ] && dd if=/dev/zero of=\"$f\" bs=8192 count=1 conv=notrunc status=none 2>/dev/null; done; rm -rf " + fanoutKeyDir
		if wipeErr := remotessh.RunSSHStreaming(hub, wipe); wipeErr != nil {
			err = errors.Join(err, fmt.Errorf("wipe the fanout keys on hub %s: %w", hub.Host, wipeErr))
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
	errs := make([]error, len(remaining))
	for i, target := range remaining {
		wg.Add(1)
		go func(idx int, target inspector.Node) {
			defer wg.Done()
			if err := fanOutTo(hub, hubPath, target, stager); err != nil {
				errs[idx] = err
				return
			}
			fmt.Printf("  ✓ %s done\n", target.Host)
		}(i, target)
	}
	wg.Wait()

	var failed []string
	for i, err := range errs {
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", remaining[i].Host, err)
			failed = append(failed, remaining[i].Host)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("push failed on %d node(s): %s\n  %s", len(failed), strings.Join(failed, ", "), stageHint)
	}
	fmt.Printf("\n✓ Push complete (%d nodes)\n", len(remaining)+1)
	return nil
}

// fanOutTo copies the archive from the hub into a private directory on
// target, using target's staged key only, and stages it there.
func fanOutTo(hub inspector.Node, hubPath string, target inspector.Node, stager *nodeStager) error {
	keyPath := fanoutKeyDir + "/" + target.Host
	dir, err := makeUploadDir(func(cmd string) (string, error) {
		return remotessh.RunSSHOutput(hub, sshVia(target, keyPath, cmd))
	})
	if err != nil {
		return fmt.Errorf("fanout to %s failed: %w", target.Host, err)
	}
	scpCmd := fmt.Sprintf("scp %s -i %s %s %s@%s:%s", fanoutSSHOptions, keyPath, hubPath, target.User, target.Host, uploadPath(dir))
	if err := remotessh.RunSSHStreaming(hub, scpCmd); err != nil {
		if rmErr := remotessh.RunSSHStreaming(hub, sshVia(target, keyPath, remotessh.SudoPrefix(target)+"rm -rf "+dir)); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove the upload %s on %s: %w", dir, target.Host, rmErr))
		}
		return fmt.Errorf("fanout to %s failed: %w", target.Host, err)
	}
	if err := remotessh.RunSSHStreaming(hub, sshVia(target, keyPath, stager.stageAndRemove(remotessh.SudoPrefix(target), dir))); err != nil {
		return fmt.Errorf("stage on %s failed: %w", target.Host, err)
	}
	return nil
}

// stageCommand is the node-side step: the node's installed orama verifies the
// uploaded archive against its trust anchor and only then puts it in place.
// It used to be a bare `tar xzf -C /opt/orama`, which replaced the binaries
// systemd runs from /opt/orama/bin before anything had checked them.
func stageCommand(sudo, remotePath string, trust []string) string {
	cmd := fmt.Sprintf("%s%s node stage-archive --archive %s", sudo, NodeOramaBinary, remotePath)
	if len(trust) > 0 {
		cmd += " --trust-signers " + strings.Join(trust, ",")
	}
	return cmd
}

// stageAndRemove stages the archive uploaded to dir and removes dir whether or
// not the stage succeeded, exiting with the stage's status.
func stageAndRemove(sudo, dir string, trust []string) string {
	return fmt.Sprintf("%s; rc=$?; %srm -rf %s; exit $rc", stageCommand(sudo, uploadPath(dir), trust), sudo, dir)
}
