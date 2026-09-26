package push

import (
	"errors"
	"fmt"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"os"
	"path/filepath"
	"strings"

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
	Direct bool   // Kept so existing invocations still parse. Every push uploads from this machine; node SSH keys are never copied to another node.
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

// ToNodes pushes the archive at archivePath to nodes from this machine.
// direct is accepted and ignored: there is no hub fan-out, because that
// copied each node's SSH key onto the hub.
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
	// Every node is reached from this machine, with the key that already opens
	// it. An earlier fan-out copied those keys onto the hub so the hub could
	// ssh onward; a key on the hub is a key the hub's user can read.
	return pushDirect(nodes, stager)
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
