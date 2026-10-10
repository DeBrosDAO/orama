package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	psetup "github.com/DeBrosOfficial/network/cmd/orama/internal/production/setup"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

const (
	// releaseFetchBudget bounds the download on one machine: an archive of a few
	// hundred megabytes over a slow link.
	releaseFetchBudget = 40 * time.Minute
	// fetchConnectSeconds, fetchStallSeconds and fetchStallBytes are curl's limits
	// on a download that does not move: no connection in 30 s, or less than 1 KiB/s
	// for a minute, ends it with an error instead of leaving it to the budget.
	fetchConnectSeconds = 30
	fetchStallSeconds   = 60
	fetchStallBytes     = 1024
	// fetchMaxRedirects: GitHub release assets redirect once or twice.
	fetchMaxRedirects = 5
	// downloadedName, endorsedManifestName and endorsedSignatureName are the files in
	// the machine's archive directory; archive.tar.gz is what StageArchiveCommand
	// stages.
	downloadedName        = "release.tar.gz"
	endorsedManifestName  = "endorsed-manifest.json"
	endorsedSignatureName = "endorsed-manifest.sig"
	madeArchiveName       = "archive.tar.gz"
	// markers split the download script's output.
	markReleaseDir = "__RELEASE_DIR__"
	markReleaseSum = "__RELEASE_SHA256__"
)

// fetchReleaseScript, as root on a machine that has no orama yet: makes the
// private directory the archive will be staged from, downloads ref.URL into it
// with curl, and refuses the file unless it has the length and the SHA-256 the
// signed targets metadata gives. The directory is removed when anything fails,
// and kept, with its path and the digest printed, when the file is the signed
// one. Nothing from the file is run or extracted here.
//
// Every value interpolated is quoted or a number: the URL was built by the
// repository client from a validated target name, and the digest is hex.
func fetchReleaseScript(ref *ReleaseRef) (string, error) {
	u, err := url.Parse(ref.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("the release URL %q is not an http(s) URL", ref.URL)
	}
	if !sha256Hex.MatchString(ref.SHA256) {
		return "", fmt.Errorf("the release digest %q is not a SHA-256", ref.SHA256)
	}
	if ref.Length <= 0 {
		return "", fmt.Errorf("the release length %d is not a size", ref.Length)
	}
	length := strconv.FormatInt(ref.Length, 10)
	file := `"$dir/` + downloadedName + `"`
	return strings.Join([]string{
		"set -eu -o pipefail",
		"dir=$(mktemp -d " + psetup.ArchiveDirTemplate + ")",
		"ok=0",
		`trap '[ "$ok" = 1 ] || rm -rf "$dir"' EXIT`,
		// The protocol is the repository's own; a redirect may only go to https, so a
		// repository cannot send the machine to a plain-HTTP mirror.
		fmt.Sprintf("curl --fail --silent --show-error --location --proto %s --proto-redir '=https' --max-redirs %d "+
			"--connect-timeout %d --speed-limit %d --speed-time %d --max-filesize %s --output %s %s",
			clusterops.ShellQuote("="+u.Scheme), fetchMaxRedirects, fetchConnectSeconds, fetchStallBytes, fetchStallSeconds,
			length, file, clusterops.ShellQuote(ref.URL)),
		"size=$(stat -c %s " + file + ")",
		fmt.Sprintf(`[ "$size" = %s ] || { echo "the download is $size bytes, the signed metadata says %s" >&2; exit 1; }`, length, length),
		"sum=$(sha256sum " + file + " | cut -d' ' -f1)",
		fmt.Sprintf(`[ "$sum" = %s ] || { echo "the download has sha256 $sum, the signed metadata says %s" >&2; exit 1; }`, ref.SHA256, ref.SHA256),
		"ok=1",
		"echo " + markReleaseDir + `; echo "$dir"; echo ` + markReleaseSum + `; echo "$sum"`,
	}, "\n"), nil
}

// assembleEndorsedScript, as root: makes the archive the operator endorsed from
// the one downloaded into dir. The downloaded archive is unpacked into a private
// tree, its manifest.json replaced by the endorsed one and its manifest.sig
// added, and the tree packed as dir/archive.tar.gz, which is where
// StageArchiveCommand stages it from. The names in it have no leading "./": the
// stage extracts bin/orama by that name.
//
// The archive was checked against the signed targets before it was unpacked, so
// the tree is the release; whether its files match the endorsed manifest is what
// the stage verifies next.
func assembleEndorsedScript(dir string) string {
	return strings.Join([]string{
		"set -eu -o pipefail",
		"umask 022",
		"cd " + dir,
		"mkdir -m 700 tree",
		"tar --no-same-owner --no-same-permissions -xzf " + downloadedName + " -C tree",
		"rm " + downloadedName,
		"install -m 644 " + endorsedManifestName + " tree/" + archivetrust.ManifestName,
		"install -m 644 " + endorsedSignatureName + " tree/" + archivetrust.SignatureName,
		"(cd tree && tar --hard-dereference --numeric-owner --owner=0 --group=0 -cf - -- *) | gzip -1 > " + madeArchiveName,
		"rm -rf tree " + endorsedManifestName + " " + endorsedSignatureName,
	}, "\n")
}

// FetchRelease has the machine download the release and check it against the
// signed digest, then reads the archive's manifest from it for the operator to
// sign. The machine's own check is the first; the digest it reports is checked
// again by the caller.
func (m *sshMachine) FetchRelease(ctx context.Context, ref *ReleaseRef) (*FetchedRelease, error) {
	script, err := fetchReleaseScript(ref)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, releaseFetchBudget)
	defer cancel()
	out, err := m.capture(ctx, bash(m.sudo(), script), nil)
	if err != nil {
		return nil, err
	}
	sec := splitMarked(out, markReleaseDir, markReleaseSum)
	dir, sum := strings.TrimSpace(sec[markReleaseDir]), strings.TrimSpace(sec[markReleaseSum])
	if !psetup.ValidArchiveDir(dir) || !sha256Hex.MatchString(sum) {
		return nil, fmt.Errorf("the machine answered the download with directory %q and digest %q", dir, sum)
	}
	f := &FetchedRelease{Dir: dir, SHA256: sum}
	manifest, err := m.capture(ctx, bash(m.sudo(), "tar -xzOf "+dir+"/"+downloadedName+" "+archivetrust.ManifestName), nil)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read the manifest of the downloaded archive: %w", err), m.DiscardFetched(context.WithoutCancel(ctx), f))
	}
	f.Manifest = []byte(manifest)
	return f, nil
}

// StageFetched gives the machine the operator's signed manifest, has it make the
// endorsed archive from the download, and stages that archive the way an upload
// is staged: the CLI is extracted alone, checked against the signed manifest's
// checksum, and run to verify the archive against the operator's wallet and put
// it in place.
func (m *sshMachine) StageFetched(ctx context.Context, f *FetchedRelease, e *Endorsement) error {
	stage, err := psetup.StageArchiveCommand(f.Dir, e.CLISHA256, []string{m.wallet})
	if err != nil {
		return err
	}
	signed := []struct {
		name string
		body []byte
	}{{endorsedManifestName, e.Manifest}, {endorsedSignatureName, []byte(e.Signature)}}
	for _, file := range signed {
		write := bash(m.sudo(), "set -eu; umask 077; cat > "+f.Dir+"/"+file.name)
		if err := m.sh.Run(ctx, write, bytes.NewReader(file.body), nil); err != nil {
			return fmt.Errorf("put %s on the machine: %w", file.name, err)
		}
	}
	if err := m.stream(ctx, bash(m.sudo(), assembleEndorsedScript(f.Dir)), nil); err != nil {
		return fmt.Errorf("make the endorsed archive from the download: %w", err)
	}
	return m.stream(ctx, stage, nil)
}

// DiscardFetched removes the machine's archive directory.
func (m *sshMachine) DiscardFetched(ctx context.Context, f *FetchedRelease) error {
	if !psetup.ValidArchiveDir(f.Dir) {
		return fmt.Errorf("%q is not an archive directory of this run", f.Dir)
	}
	if err := m.sh.Run(ctx, bash(m.sudo(), "rm -rf "+f.Dir), nil, nil); err != nil {
		return fmt.Errorf("remove %s on %s: %w", f.Dir, m.node.Host, err)
	}
	return nil
}
