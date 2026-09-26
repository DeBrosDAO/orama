package push

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// Staging a push.
//
// A push normally stages each node's copy with the node's installed orama
// (NodeOramaBinary node stage-archive), which verifies it against the node's
// trust anchor: nothing from the archive runs before the node's own binary has
// checked it.
//
// That needs a node whose CLI has stage-archive and an anchor. A push with
// --trust-signers is the one for nodes that have neither — 0.122.x nodes, whose
// CLI predates archive signing — so it cannot rely on the node's CLI. It stages
// the way `orama node install --remote` does: the archive is verified here,
// against --trust-signers, and what is uploaded is a canonical archive written
// from the verified tree; on the node, the CLI alone is extracted into a
// root-only directory under /opt/orama, checked against the checksum the
// verified manifest lists, and that CLI runs stage-archive — which verifies
// the archive again against the node's anchor, or against --trust-signers when
// there is none and writes the anchor from them.

// nodeStager is how every node of one push stages the archive it is given.
type nodeStager struct {
	// archive is the local file uploaded to the nodes.
	archive string
	// trust is --trust-signers, normalized; empty stages with the node's CLI.
	trust []string
	// cliSum is the verified manifest's checksum of bin/orama (lowercase hex),
	// set when trust is.
	cliSum string
}

// cliSumPattern is a sha256 in lowercase hex.
var cliSumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// signerPattern is a normalized signer address.
var signerPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// archiveCLIName is the orama CLI in an archive's bin/.
const archiveCLIName = "orama"

// newNodeStager prepares a push of archivePath. With trust it verifies the
// archive here and returns the canonical copy to upload; the cleanup removes
// that copy.
func newNodeStager(archivePath string, trust []string) (*nodeStager, func() error, error) {
	if len(trust) == 0 {
		return &nodeStager{archive: archivePath}, func() error { return nil }, nil
	}
	for _, s := range trust {
		if !signerPattern.MatchString(s) {
			return nil, nil, fmt.Errorf("--trust-signers: %q is not an address", s)
		}
	}
	upload, err := archivetrust.PrepareUpload(archivePath, trust)
	if err != nil {
		return nil, nil, fmt.Errorf("refusing to push %s: %w", archivePath, err)
	}
	m := upload.Verified.Manifest
	cliSum := strings.ToLower(m.Checksums[archiveCLIName])
	if !cliSumPattern.MatchString(cliSum) {
		return nil, nil, errors.Join(fmt.Errorf("the verified archive lists no sha256 for bin/%s", archiveCLIName), upload.Remove())
	}
	fmt.Printf("Archive verified here: v%s linux/%s signed by %s\n", m.Version, m.Arch, upload.Verified.Signer)
	return &nodeStager{archive: upload.Path, trust: trust, cliSum: cliSum}, upload.Remove, nil
}

// stage is the node-side command that stages the archive at remotePath.
func (s *nodeStager) stage(sudo, remotePath string) string {
	if len(s.trust) == 0 {
		return stageCommand(sudo, remotePath, nil)
	}
	return archiveCLIStage(sudo, remotePath, s.cliSum, s.trust, "")
}

// stageAndRemove stages the archive uploaded to dir and removes dir either way.
func (s *nodeStager) stageAndRemove(sudo, dir string) string {
	if len(s.trust) == 0 {
		return stageAndRemove(sudo, dir, nil)
	}
	return archiveCLIStage(sudo, uploadPath(dir), s.cliSum, s.trust, dir)
}

// archiveCLIStage is the node-side command that stages the archive at
// archivePath with the CLI inside it, as root. removeDir, when set, is removed
// on exit whatever happens.
//
// The script travels base64-encoded on a pipe into `bash -s`: it holds single
// quotes, and the fanout wraps each node's command in single quotes for the
// hub's ssh. Every value interpolated is checked here: archivePath and
// removeDir come from uploadDirPattern paths, cliSum is hex, the signers are
// addresses.
func archiveCLIStage(sudo, archivePath, cliSum string, trust []string, removeDir string) string {
	cleanup := `rm -rf "$cli"`
	if removeDir != "" {
		cleanup += " " + removeDir
	}
	script := strings.Join([]string{
		"set -eu",
		"mkdir -p /opt/orama",
		// /opt/orama must be root's alone before a binary is run from under
		// it: anyone else who could write it could swap the checked CLI.
		// Fails closed: a find that cannot inspect it prints nothing.
		`bad=$(find /opt/orama -maxdepth 0 \( ! -user root -o -perm -020 -o -perm -002 \) -print) || ` +
			`{ echo "cannot inspect /opt/orama" >&2; exit 1; }`,
		`[ -z "$bad" ] || { echo "/opt/orama is not owned by root and writable only by root" >&2; exit 1; }`,
		"cli=$(mktemp -d /opt/orama/" + SetupCLIPrefix + "XXXXXXXX)",
		"trap '" + cleanup + "' EXIT",
		`tar --no-same-owner -xzf ` + archivePath + ` -C "$cli" bin/` + archiveCLIName,
		// The archive decides what bin/orama is: a symlink would make the
		// checksum below, and the exec after it, follow it anywhere.
		`[ -f "$cli/bin/` + archiveCLIName + `" ] && [ ! -L "$cli/bin/` + archiveCLIName + `" ] || ` +
			`{ echo "bin/` + archiveCLIName + ` in the archive is not a regular file" >&2; exit 1; }`,
		`echo "` + cliSum + `  $cli/bin/` + archiveCLIName + `" | sha256sum -c --quiet -`,
		`"$cli/bin/` + archiveCLIName + `" node stage-archive --archive ` + archivePath + ` --trust-signers ` + strings.Join(trust, ","),
	}, "\n") + "\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	return "printf %s " + encoded + " | base64 -d | " + sudo + "bash -s"
}
