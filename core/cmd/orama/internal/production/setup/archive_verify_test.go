package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

const operatorWallet = "0x1111111111111111111111111111111111111111"

// A new machine runs the orama binary out of the uploaded archive, so nothing
// on it can check that archive first. Setup checks it before uploading.
func TestEnsureArchive_refusesAnArchiveThatDoesNotVerifyBeforeUploading(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "orama-0.0.0-linux-amd64.tar.gz")
	if err := os.WriteFile(bogus, []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An empty node: any SSH attempt would fail with a different error.
	err := EnsureArchive(inspector.Node{}, bogus, []string{operatorWallet})
	if err == nil || !strings.Contains(err.Error(), "refusing to upload") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildInstallCommand_alwaysPassesTheOperatorWallet(t *testing.T) {
	cmd, err := buildInstallCommand(Options{IP: "203.0.113.5", Genesis: true, BaseDomain: "example.com"}, operatorWallet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "--operator-wallet '"+operatorWallet+"'") {
		t.Fatalf("the genesis install would have no archive trust anchor: %s", cmd)
	}
}

// A joining node gets the signers it must be sent and the invite it joins
// with, each quoted for the root shell it runs in.
func TestInstallCommand_joinCarriesTheExpectedSignersAndTheInvite(t *testing.T) {
	cmd := InstallCommand(Options{IP: "203.0.113.6", BaseDomain: "example.com", User: "root"},
		operatorWallet, []string{operatorWallet}, "orama1_abc")
	for _, want := range []string{
		"--operator-wallet '" + operatorWallet + "'",
		"--expect-archive-signers '" + operatorWallet + "'",
		"--token 'orama1_abc'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("join install command lacks %s: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "--join ") {
		t.Fatalf("the invite names the node to join; an explicit --join would override its pin: %s", cmd)
	}
}

// A genesis node joins nothing: no invite and no expected signers.
func TestInstallCommand_genesisHasNoJoinFlags(t *testing.T) {
	cmd := InstallCommand(Options{IP: "203.0.113.5", Genesis: true}, operatorWallet, []string{operatorWallet}, "orama1_abc")
	if strings.Contains(cmd, "--token") || strings.Contains(cmd, "--expect-archive-signers") {
		t.Fatalf("a genesis install carries join flags: %s", cmd)
	}
}

// An empty archive path is refused before any node is touched, for every node.
func TestEnsureArchives_needsAnArchive(t *testing.T) {
	err := EnsureArchives([]inspector.Node{{Host: "203.0.113.5"}, {Host: "203.0.113.6"}}, "", []string{operatorWallet})
	if err == nil || !strings.Contains(err.Error(), "--archive is required") {
		t.Fatalf("EnsureArchives without an archive: %v", err)
	}
}

const testCLISum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// The node has no verified binary on a first install, so the CLI extracted
// from the upload must match the verified manifest before it runs; the archive
// then goes in through stage-archive (verification, lock, crash-safe swap),
// never `rm -rf; cp -a`.
func TestStageArchiveCommand_checksTheCLIThenStagesThroughIt(t *testing.T) {
	cmd := stageArchiveCommand("/tmp/orama-archive.AbC12345", testCLISum, []string{operatorWallet})
	order := []string{
		"tar --no-same-owner -xzf /tmp/orama-archive.AbC12345/archive.tar.gz -C /opt/orama/.archive-cli-AbC12345 bin/orama",
		// The archive decides what bin/orama is; a symlink is refused before
		// the checksum follows it.
		"[ ! -L /opt/orama/.archive-cli-AbC12345/bin/orama ]",
		"echo \"" + testCLISum + "  /opt/orama/.archive-cli-AbC12345/bin/orama\" | sha256sum -c --quiet -",
		"/opt/orama/.archive-cli-AbC12345/bin/orama node stage-archive --archive /tmp/orama-archive.AbC12345/archive.tar.gz",
	}
	last := -1
	for _, want := range order {
		i := strings.Index(cmd, want)
		if i < 0 || i < last {
			t.Fatalf("missing or out of order %q in:\n%s", want, cmd)
		}
		last = i
	}
	if strings.Index(cmd, "find /opt/orama -maxdepth 0") > strings.Index(cmd, "sha256sum -c") {
		t.Errorf("/opt/orama's ownership must be checked before the CLI is extracted and run:\n%s", cmd)
	}
	for _, want := range []string{"set -e", "trap \"rm -rf /tmp/orama-archive.AbC12345 /opt/orama/.archive-cli-AbC12345\" EXIT",
		"find /opt/orama -maxdepth 0 \\( ! -user root -o -perm -020 -o -perm -002 \\) -print) || ",
		"cannot inspect /opt/orama",
		"--trust-signers " + operatorWallet, "if [ -e /etc/orama/archive-signers ]"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("missing %q in:\n%s", want, cmd)
		}
	}
	for _, forbidden := range []string{"cp -a", "rm -rf /opt/orama/bin", "/opt/orama/.orama"} {
		if strings.Contains(cmd, forbidden) {
			t.Errorf("the stage command must not %q: %s", forbidden, cmd)
		}
	}
}

func TestGoArch_mapsWhatUnameReports(t *testing.T) {
	for machine, want := range map[string]string{"x86_64": "amd64", "aarch64": "arm64"} {
		if goArch[machine] != want {
			t.Errorf("uname -m %q -> %q, want %q", machine, goArch[machine], want)
		}
	}
}

func TestExpectedArchiveSigners(t *testing.T) {
	wallet := []string{operatorWallet}
	if got, err := expectedArchiveSigners(Options{Genesis: true}, wallet); err != nil || got != nil {
		t.Errorf("genesis: %v, %v", got, err)
	}
	if got, err := expectedArchiveSigners(Options{Gateway: "https://gw.example"}, wallet); err != nil || len(got) != 1 || got[0] != operatorWallet {
		t.Errorf("gateway join: %v, %v", got, err)
	}
	if _, err := expectedArchiveSigners(Options{JoinVia: "not-user-at-ip"}, wallet); err == nil {
		t.Error("a malformed --join-via was accepted")
	}
}
