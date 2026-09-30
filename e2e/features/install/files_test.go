//go:build e2e_fleet

package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// findNone fails when the find expression matches anything on n: each is a
// permission the install promises never exists.
func findNone(t testing.TB, f *fleet.Fleet, n fleet.Node, what, cmd string) {
	t.Helper()
	out := f.MustExec(t, n, cmd)
	if s := strings.TrimSpace(out.Stdout); s != "" {
		t.Errorf("%s: %s:\n%s", n.Name, what, s)
	}
}

// TestInstall_wireguardConfPrivate: wg0.conf holds the node's private key
// and is 0600 root (docs/SECURITY.md "wg0.conf is chmod 0600 after write"),
// in a directory the orama user cannot write (the node unit keeps
// /etc/wireguard read-only: wg-quick runs its PostUp as root).
func TestInstall_wireguardConfPrivate(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		infra.RequireStat(t, f, n, infra.WireGuardConfPath, "root", "root", "600")
		findNone(t, f, n, "/etc/wireguard is writable by someone other than root",
			"find /etc/wireguard -maxdepth 0 \\( ! -user root -o -perm -020 -o -perm -002 \\) -print")
	}
}

// TestInstall_secretsPrivate: the secrets directory is 0700 and every secret
// in it 0600, owned by the orama user, with no link a reader could be
// redirected through (docs/SECURITY.md; core/pkg/install/config.go writes
// each secret 0600 into a 0700 dir).
func TestInstall_secretsPrivate(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		infra.RequireStat(t, f, n, infra.OramaSecretsDir, "orama", "orama", "700")
		dir := fleet.ShellQuote(infra.OramaSecretsDir)
		findNone(t, f, n, "a secret readable or writable by group or others", "find "+dir+" -mindepth 1 -perm /077 -print")
		findNone(t, f, n, "a secret not owned by orama", "find "+dir+" -mindepth 1 ! -user orama -print")
		findNone(t, f, n, "a symlink among the secrets", "find "+dir+" -mindepth 1 -type l -print")
		for _, s := range []string{infra.NodeKeyPath, infra.RQLiteAuthSecret} {
			infra.RequireStat(t, f, n, s, "orama", "orama", "600")
		}
	}
}

// TestInstall_binariesRootOwned: /opt/orama is root's alone, and
// /opt/orama/bin and every binary in it are root:orama 0750: the orama user
// runs them and cannot replace them (docs/SECURITY.md "Dedicated User").
func TestInstall_binariesRootOwned(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		findNone(t, f, n, "/opt/orama is not root's alone",
			"find /opt/orama -maxdepth 0 \\( ! -user root -o -perm -020 -o -perm -002 \\) -print")
		infra.RequireStat(t, f, n, infra.OramaBinDir, "root", "orama", "750")
		bin := fleet.ShellQuote(infra.OramaBinDir)
		findNone(t, f, n, "a binary not root:orama 0750",
			"find "+bin+" -mindepth 1 \\( ! -user root -o ! -group orama -o ! -perm 750 \\) -print")
		findNone(t, f, n, "a staging leftover under /opt/orama",
			"find /opt/orama -maxdepth 1 \\( -name '.archive-staging-*' -o -name '.archive-cli-*' \\) -print")
		if out := f.Exec(t, n, "runuser -u orama -- test -w "+bin); out.Exit == 0 {
			t.Errorf("%s: the orama user can write %s", n.Name, infra.OramaBinDir)
		}
	}
}

// TestInstall_archiveTrustAnchor: /etc/orama/archive-signers is root:root
// 0644 and trusts exactly the operator's wallet, one lowercase address per
// line (docs/SECURITY.md "Supply Chain": a genesis install creates the anchor
// from --operator-wallet; a joiner takes the list from the cluster).
func TestInstall_archiveTrustAnchor(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	want := strings.ToLower(f.State.OperatorAddress)
	for _, n := range f.State.Nodes {
		infra.RequireStat(t, f, n, infra.ArchiveSigners, "root", "root", "644")
		lines := strings.Fields(string(f.ReadFile(t, n, infra.ArchiveSigners)))
		if len(lines) != 1 || lines[0] != want {
			t.Errorf("%s: the anchor trusts %v, want exactly [%s]", n.Name, lines, want)
		}
		findNone(t, f, n, "/etc/orama is writable by someone other than root",
			"find /etc/orama -maxdepth 0 \\( ! -user root -o -perm -020 -o -perm -002 \\) -print")
	}
}

// TestInstall_stagedBuildIsSigned: the build in /opt/orama is the run's
// archive: its manifest is the archive's, byte for byte, and its signature
// is there beside it.
func TestInstall_stagedBuildIsSigned(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	if prev := f.State.PreviousArchivePath; prev != "" {
		old := infra.ReadArchiveFile(t, prev, infra.ManifestName)
		if string(f.ReadFile(t, f.State.Nodes[0], infra.StagedManifest)) == string(old) {
			harness.SkipNotApplicable(t, "the fleet runs the previous release (E2E_INSTALL_PREVIOUS); stage 10 upgrades it to HEAD and rollout-upgrade checks the staged build then")
		}
	}
	want := infra.ReadArchiveFile(t, f.State.ArchivePath, infra.ManifestName)
	sig := strings.TrimSpace(string(infra.ReadArchiveFile(t, f.State.ArchivePath, infra.SignatureName)))
	for _, n := range f.State.Nodes {
		if got := f.ReadFile(t, n, infra.StagedManifest); string(got) != string(want) {
			t.Errorf("%s: /opt/orama/manifest.json is not the run's archive manifest", n.Name)
		}
		if got := strings.TrimSpace(string(f.ReadFile(t, n, infra.StagedSignature))); got != sig {
			t.Errorf("%s: /opt/orama/manifest.sig is not the run's archive signature", n.Name)
		}
	}
}

// TestInstall_privhelperSocket: the privileged helper's socket is root:orama
// 0660 and socket-activated (docs/SECURITY.md: only root and the orama group
// connect; each connection runs one helper instance as root).
func TestInstall_privhelperSocket(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		st, ok := infra.StatFile(t, f, n, infra.PrivhelperSock)
		if !ok {
			t.Errorf("%s: %s does not exist", n.Name, infra.PrivhelperSock)
			continue
		}
		if st.Owner != "root" || st.Group != "orama" || st.Mode != "660" || st.Type != "socket" {
			t.Errorf("%s: %s is %s:%s %s %s, want root:orama 660 socket", n.Name, infra.PrivhelperSock, st.Owner, st.Group, st.Mode, st.Type)
		}
		if s := f.Unit(t, n, infra.PrivhelperSocket); s != "active" {
			t.Errorf("%s: %s is %s", n.Name, infra.PrivhelperSocket, s)
		}
		if out := f.Exec(t, n, "runuser -u nobody -- test -w "+infra.PrivhelperSock); out.Exit == 0 {
			t.Errorf("%s: an unrelated user can write the privileged helper's socket", n.Name)
		}
	}
}
