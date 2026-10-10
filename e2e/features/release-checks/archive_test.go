//go:build e2e_fleet

package releasechecks

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// shippedBinaries are what every build must carry (docs/CLI_REFERENCE.md
// "orama maint build": the Orama binaries, Olric, IPFS Kubo, IPFS Cluster, RQLite,
// CoreDNS, Caddy).
var shippedBinaries = []string{"orama", "orama-node", "gateway", "olric-server", "ipfs", "ipfs-cluster-service", "rqlited", "coredns", "caddy"}

// TestRelease_headArchiveVerifies: the archive the run built with `orama
// build` verifies on its own: its signature recovers to the operator's
// wallet, every file matches the signed manifest with none unlisted, it is
// built for linux/amd64, it carries every shipped binary, and its version is
// the CLI's (docs/SECURITY.md "Supply Chain").
func TestRelease_headArchiveVerifies(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	harness.RequireArchive(t, f.State.ArchivePath)
	dir := filepath.Join(t.TempDir(), "archive")
	if err := archivetrust.Extract(f.State.ArchivePath, dir); err != nil {
		t.Fatalf("extract %s: %v", f.State.ArchivePath, err)
	}
	v, err := archivetrust.VerifyIntegrity(dir)
	if err != nil {
		t.Fatalf("the HEAD archive does not verify: %v", err)
	}
	if v.Signer != strings.ToLower(f.State.OperatorAddress) {
		t.Errorf("signed by %s, want the operator %s", v.Signer, f.State.OperatorAddress)
	}
	if v.Manifest.Arch != "amd64" {
		t.Errorf("built for linux/%s, the fleet is linux/amd64", v.Manifest.Arch)
	}
	for _, b := range shippedBinaries {
		if _, ok := v.Manifest.Checksums[b]; !ok {
			t.Errorf("the manifest lists no %s", b)
		}
	}
	fields := strings.Fields(harness.CLI(t).MustOK(t, "version").Stdout)
	if len(fields) < 2 || fields[1] != v.Manifest.Version {
		t.Errorf("the archive is version %s, the CLI says %v", v.Manifest.Version, fields)
	}
	if v.Manifest.Commit == "" || v.Manifest.Date == "" {
		t.Errorf("the manifest names no commit or date: %+v", v.Manifest)
	}
}

// TestRelease_nodesRunTheManifestBinaries: every binary under /opt/orama/bin
// on every node, and the orama CLI on its PATH, is byte for byte the one the
// signed manifest of the build it runs lists.
func TestRelease_nodesRunTheManifestBinaries(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	m := infra.ArchiveManifest(t, infra.RunningArchive(t, f))
	for _, n := range f.State.Nodes {
		out := f.MustExec(t, n, "cd "+infra.OramaBinDir+" && sha256sum *").Stdout
		seen := 0
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fs := strings.Fields(line)
			if len(fs) != 2 {
				continue
			}
			want, ok := m.Checksums[fs[1]]
			if !ok {
				t.Errorf("%s: /opt/orama/bin/%s is not in the signed manifest", n.Name, fs[1])
				continue
			}
			seen++
			if !strings.EqualFold(want, fs[0]) {
				t.Errorf("%s: /opt/orama/bin/%s does not match the signed manifest", n.Name, fs[1])
			}
		}
		if seen == 0 {
			t.Errorf("%s: no binary in /opt/orama/bin", n.Name)
		}
		cli := strings.Fields(f.MustExec(t, n, "sha256sum /usr/local/bin/orama").Stdout)
		if len(cli) == 0 || !strings.EqualFold(cli[0], m.Checksums["orama"]) {
			t.Errorf("%s: /usr/local/bin/orama is not the signed build's CLI", n.Name)
		}
	}
}

// TestRelease_versionEverywhereAgrees: the public /v1/version, each node's
// `orama version` and the build the nodes run name the same release.
func TestRelease_versionEverywhereAgrees(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	m := infra.ArchiveManifest(t, infra.RunningArchive(t, f))
	var v struct {
		Version string `json:"version"`
	}
	if err := harness.GW(t).MustSend(t, gw.Req{Path: "/v1/version"}).Expect(t, http.StatusOK).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Version != m.Version {
		t.Errorf("/v1/version says %s, the nodes run %s", v.Version, m.Version)
	}
	for _, n := range f.State.Nodes {
		out := infra.OnNode(t, f, n, "version")
		if fs := strings.Fields(out.Stdout); len(fs) < 2 || fs[1] != m.Version {
			t.Errorf("%s: orama version printed %q, want %s", n.Name, out.Stdout, m.Version)
		}
	}
}
