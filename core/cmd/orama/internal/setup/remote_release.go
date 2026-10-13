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
	// releaseCleanupBudget bounds removing a download, which does not use the run's
	// context: that may be the reason it is removed.
	releaseCleanupBudget = time.Minute
	// fetchConnectSeconds, fetchStallSeconds and fetchStallBytes are curl's limits
	// on a download that does not move: no connection in 30 s, or less than 1 KiB/s
	// for a minute, ends it with an error instead of leaving it to the budget.
	fetchConnectSeconds = 30
	fetchStallSeconds   = 60
	fetchStallBytes     = 1024
	// fetchMaxRedirects: GitHub release assets redirect once or twice.
	fetchMaxRedirects = 5
	// releaseSpaceFactor is how many times the archive the filesystem of the download
	// must have free: the download, its unpacked tree (a release compresses about
	// 2.3 to 1) and the archive packed again, with the download gone by then.
	releaseSpaceFactor = 4
	// fileLimitBlock is the unit `ulimit -f` is told in. bash counts 1024 bytes, POSIX
	// mode 512; the smaller is assumed, so the cap is never below the archive.
	fileLimitBlock = 512
	// fileLimitSlack is the blocks the cap has above the archive: enough that an
	// archive of exactly the signed length is written whole, and a longer one is cut
	// at the cap and found too long by the size check.
	fileLimitSlack = 2
	// downloadedName, endorsedManifestName and endorsedSignatureName are the files in
	// the machine's archive directory; archive.tar.gz is what StageArchiveCommand
	// stages.
	downloadedName        = "release.tar.gz"
	endorsedManifestName  = "endorsed-manifest.json"
	endorsedSignatureName = "endorsed-manifest.sig"
	madeArchiveName       = "archive.tar.gz"
	// markReleaseSum precedes the digest the download script prints.
	markReleaseSum = "__RELEASE_SHA256__"
)

// fetchTools are the programs the scripts of a release run on a machine, and the
// package that has each one on Debian and Ubuntu.
var fetchTools = []struct{ tool, pkg string }{
	{"curl", "curl"}, {"tar", "tar"}, {"gzip", "gzip"}, {"awk", "gawk"}, {"find", "findutils"},
	{"sha256sum", "coreutils"}, {"stat", "coreutils"}, {"df", "coreutils"}, {"install", "coreutils"}, {"mkdir", "coreutils"},
}

// toolCheck fails the script at its start, naming the package, when a program it
// runs is missing.
func toolCheck() string {
	lines := make([]string, 0, len(fetchTools))
	for _, t := range fetchTools {
		lines = append(lines, fmt.Sprintf(`command -v %s >/dev/null 2>&1 || { echo "this machine has no %s; install it (apt-get install %s) and run setup again" >&2; exit 1; }`, t.tool, t.tool, t.pkg))
	}
	return strings.Join(lines, "\n")
}

// fetchReleaseScript, as root on a machine that has no orama yet: makes the
// private directory dir, which this computer named, checks the filesystem has
// room for the release, downloads ref.URL into it with curl, and refuses the file
// unless it has the length and the SHA-256 the signed targets metadata gives. The
// directory is removed when anything fails, and kept, with the digest printed,
// when the file is the signed one. Nothing from the file is run or extracted here.
//
// The download is bounded whatever the server sends: curl stops at the length it
// was told, and `ulimit -f` caps what the process can write when the server gives
// no Content-Length. -q keeps a ~/.curlrc out of it and --globoff makes the URL
// literal. Every value interpolated is quoted or a number: dir was named by
// NewArchiveDir, the URL was built by the repository client from a validated
// target name, and the digest is hex.
func fetchReleaseScript(dir string, ref *ReleaseRef) (string, error) {
	if !psetup.ValidArchiveDir(dir) {
		return "", fmt.Errorf("%q is not an archive directory", dir)
	}
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
	needKiB := (ref.Length*releaseSpaceFactor + 1023) / 1024
	file := `"$dir/` + downloadedName + `"`
	return strings.Join([]string{
		"set -eu -o pipefail",
		toolCheck(),
		"dir=" + dir,
		"mkdir -m 700 \"$dir\"",
		"ok=0",
		`trap '[ "$ok" = 1 ] || rm -rf "$dir"' EXIT`,
		`free=$(df -Pk "$dir" | awk 'NR==2 {print $4}')`,
		fmt.Sprintf(`[ "$free" -ge %d ] || { echo "the filesystem of $dir has $((free / 1024)) MiB free, and the release needs %d MiB (%d times its size) to be unpacked and packed again" >&2; exit 1; }`,
			needKiB, needKiB/1024, releaseSpaceFactor),
		// The protocol is the repository's own; a redirect may only go to https, so a
		// repository cannot send the machine to a plain-HTTP mirror.
		fmt.Sprintf("( ulimit -f %d; curl -q --fail --silent --show-error --location --globoff --proto %s --proto-redir '=https' --max-redirs %d "+
			"--connect-timeout %d --speed-limit %d --speed-time %d --max-filesize %s --output %s %s )",
			ref.Length/fileLimitBlock+fileLimitSlack, clusterops.ShellQuote("="+u.Scheme), fetchMaxRedirects, fetchConnectSeconds,
			fetchStallBytes, fetchStallSeconds, length, file, clusterops.ShellQuote(ref.URL)),
		"size=$(stat -c %s " + file + ")",
		fmt.Sprintf(`[ "$size" = %s ] || { echo "the download is $size bytes, the signed metadata says %s" >&2; exit 1; }`, length, length),
		"sum=$(sha256sum " + file + " | cut -d' ' -f1)",
		fmt.Sprintf(`[ "$sum" = %s ] || { echo "the download has sha256 $sum, the signed metadata says %s" >&2; exit 1; }`, ref.SHA256, ref.SHA256),
		"ok=1",
		"echo " + markReleaseSum + `; echo "$sum"`,
	}, "\n"), nil
}

// assembleEndorsedScript, as root: makes the archive the operator endorsed from
// the one downloaded into dir. The member list is checked first (only regular
// files and directories, no absolute or parent-relative name), then the archive
// is unpacked into a private tree, its manifest.json replaced by the endorsed one
// and its manifest.sig added, and the tree packed as dir/archive.tar.gz, which is
// where StageArchiveCommand stages it from.
//
// The members are listed with find, not named by a glob, so a name that begins
// with a dot is kept, and with no leading "./": the stage extracts bin/orama by
// that name. The archive was checked against the signed targets before any of
// this; whether its files match the endorsed manifest is what the stage verifies
// next.
func assembleEndorsedScript(dir string) string {
	return strings.Join([]string{
		"set -eu -o pipefail",
		"umask 022",
		"cd " + dir,
		`tar -tvzf ` + downloadedName + ` | awk '{ t = substr($0, 1, 1); if (t != "-" && t != "d") { bad = 1; print "the archive has a member that is no file or directory: " $0 > "/dev/stderr" } } END { exit bad }'`,
		`tar -tzf ` + downloadedName + ` | awk '/^\// || /(^|\/)\.\.(\/|$)/ { bad = 1; print "the archive has a member with an absolute or parent name: " $0 > "/dev/stderr" } END { exit bad }'`,
		"mkdir -m 700 tree",
		"tar --no-same-owner --no-same-permissions --no-overwrite-dir -xzf " + downloadedName + " -C tree",
		"rm " + downloadedName,
		"install -m 644 " + endorsedManifestName + " tree/" + archivetrust.ManifestName,
		"install -m 644 " + endorsedSignatureName + " tree/" + archivetrust.SignatureName,
		"(cd tree && find . -mindepth 1 -printf '%P\\0') | tar --numeric-owner --owner=0 --group=0 --no-recursion --null -C tree -T - -cf - | gzip -1 > " + madeArchiveName,
		"rm -rf tree " + endorsedManifestName + " " + endorsedSignatureName,
	}, "\n")
}

// FetchRelease has the machine download the release into a directory this
// computer names, and check it against the signed digest, then reads the
// archive's manifest from it for the digest in the signed metadata to be held
// against. The directory is removed on any error: this computer names it, so it
// knows what to remove however the session ended.
func (m *sshMachine) FetchRelease(ctx context.Context, ref *ReleaseRef) (f *FetchedRelease, err error) {
	dir, err := m.archiveDir()
	if err != nil {
		return nil, err
	}
	script, err := fetchReleaseScript(dir, ref)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, releaseFetchBudget)
	defer cancel()
	defer func() {
		if err != nil {
			err = errors.Join(err, m.DiscardFetched(context.WithoutCancel(ctx), &FetchedRelease{Dir: dir}))
		}
	}()
	out, err := m.capture(ctx, bash(m.sudo(), script), nil)
	if err != nil {
		return nil, err
	}
	sum := strings.TrimSpace(splitMarked(out, markReleaseSum)[markReleaseSum])
	if !sha256Hex.MatchString(sum) {
		return nil, fmt.Errorf("the machine answered the download with digest %q", sum)
	}
	manifest, err := m.capture(ctx, bash(m.sudo(), "tar -xzOf "+dir+"/"+downloadedName+" "+archivetrust.ManifestName), nil)
	if err != nil {
		return nil, fmt.Errorf("read the manifest of the downloaded archive: %w", err)
	}
	return &FetchedRelease{Dir: dir, SHA256: sum, Manifest: []byte(manifest)}, nil
}

// archiveDir is the name of a new archive directory on the machine.
func (m *sshMachine) archiveDir() (string, error) {
	if m.newDir != nil {
		return m.newDir()
	}
	return psetup.NewArchiveDir()
}

// StageFetched gives the machine the operator's signed manifest, has it make the
// endorsed archive from the download, and stages that archive the way an upload
// is staged: the CLI is extracted alone, checked against the signed manifest's
// checksum, and run to verify the archive against the operator's wallet and put
// it in place.
//
// Until the stage command is issued the directory is this function's to remove
// when a step fails. From then on it is the stage command's: it removes the
// directory itself when it ends, and removing it from here while the stage may
// still be running would take the archive from under it.
func (m *sshMachine) StageFetched(ctx context.Context, f *FetchedRelease, e *Endorsement) error {
	stage, err := psetup.StageArchiveCommand(f.Dir, e.CLISHA256, []string{m.wallet})
	if err != nil {
		return err
	}
	if err := m.prepareEndorsed(ctx, f, e); err != nil {
		return errors.Join(err, m.DiscardFetched(context.WithoutCancel(ctx), f))
	}
	return m.stream(ctx, stage, nil)
}

// prepareEndorsed puts the signed manifest on the machine and has it make the
// endorsed archive. The directory goes into root shell text here, so it is
// checked here too, whatever the caller checked.
func (m *sshMachine) prepareEndorsed(ctx context.Context, f *FetchedRelease, e *Endorsement) error {
	if !psetup.ValidArchiveDir(f.Dir) {
		return fmt.Errorf("the archive directory %q is not one setup makes", f.Dir)
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
	return nil
}

// DiscardFetched removes the machine's archive directory.
func (m *sshMachine) DiscardFetched(ctx context.Context, f *FetchedRelease) error {
	if !psetup.ValidArchiveDir(f.Dir) {
		return fmt.Errorf("%q is not an archive directory of this run", f.Dir)
	}
	ctx, cancel := context.WithTimeout(ctx, releaseCleanupBudget)
	defer cancel()
	if err := m.sh.Run(ctx, bash(m.sudo(), "rm -rf "+f.Dir), nil, nil); err != nil {
		return fmt.Errorf("remove %s on %s: %w", f.Dir, m.node.Host, err)
	}
	return nil
}
