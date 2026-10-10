//go:build e2e_fleet

package clienvauthmisc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNetworkAdd_manifestURLsThatCannotBeTrustedAreRefused: a manifest names
// the chain and the release root a node installs from, so only an https URL
// that ends in /manifest.json is fetched, and nothing is stored when it is not
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-network-add).
func TestNetworkAdd_manifestURLsThatCannotBeTrustedAreRefused(t *testing.T) {
	t.Parallel()
	cli := isolated(t)
	before := readEnvFile(t, cli)
	for label, tc := range map[string]struct {
		url  string
		want string
	}{
		"plain http":        {"http://example.invalid/nets/x/manifest.json", "https"},
		"not a manifest":    {"https://example.invalid/nets/x/network.json", "manifest.json"},
		"not an address":    {"example", "neither a manifest URL"},
		"nothing listening": {"https://127.0.0.1:1/nets/x/manifest.json", "fetch"},
	} {
		res := run(t, cli, "network", "add", tc.url, "--yes")
		if res.Exit == exitOK || !strings.Contains(output(res), tc.want) {
			t.Errorf("network add %s (%s): exit %d, want a refusal naming %q\n%s", tc.url, label, res.Exit, tc.want, output(res))
		}
	}
	if after := readEnvFile(t, cli); len(after.Environments) != len(before.Environments) {
		t.Errorf("a refused manifest changed the configured networks: %+v -> %+v", before, after)
	}
}

// TestNetworkAdd_gatewayOptionsNeedAGateway: --ca-file and --network describe
// a gateway, --yes a manifest; mixing them is a usage error.
func TestNetworkAdd_gatewayOptionsNeedAGateway(t *testing.T) {
	t.Parallel()
	cli := isolated(t)
	for _, args := range [][]string{
		{"network", "add", "https://example.invalid/m/manifest.json", "--ca-file", "/nonexistent.pem"},
		{"network", "add", "https://example.invalid/m/manifest.json", "--network", "stagenet"},
		{"network", "add", "lab", "https://lab.example.invalid", "--yes"},
	} {
		if res := run(t, cli, args...); res.Exit != exitUsage {
			t.Errorf("orama %v: exit %d, want %d\n%s", args, res.Exit, exitUsage, output(res))
		}
	}
}

// TestMaintNetworkPublish_writesANetworkAndKeepsAPublishedGenesis: publish
// writes genesis.json, release-root.json and manifest.json, and a chain id
// that is already published refuses another genesis with the conflict code
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-maint-network-publish).
func TestMaintNetworkPublish_writesANetworkAndKeepsAPublishedGenesis(t *testing.T) {
	t.Parallel()
	cli := isolated(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	networks := filepath.Join(dir, "networks")
	genesis := write("genesis.json", `{"chain_id":"orama-e2enet-1"}`)
	root := write("root.json", `{"signed":{"_type":"root"}}`)
	res := cli.MustOK(t, "maint", "network", "publish", "--dir", networks, "--name", "e2enet", "--chain-id", "orama-e2enet-1",
		"--genesis", genesis, "--release-root", root, "--seeds", "seed1.e2enet.example.org", "--channel", "nightly",
		"--min-version", "0.3.0", "--release-repo", "https://releases.example.org/")
	if !strings.Contains(res.Stdout, "orama-e2enet-1") || !strings.Contains(res.Stdout, "sync-networks") {
		t.Errorf("publish printed:\n%s", res.Stdout)
	}
	for _, file := range []string{"manifest.json", "genesis.json", "release-root.json"} {
		if _, err := os.Stat(filepath.Join(networks, "e2enet", file)); err != nil {
			t.Errorf("publish did not write %s: %v", file, err)
		}
	}
	changed := write("changed.json", `{"chain_id":"orama-e2enet-1","app_state":{"changed":true}}`)
	again := run(t, cli, "maint", "network", "publish", "--dir", networks, "--name", "e2enet", "--chain-id", "orama-e2enet-1", "--genesis", changed)
	if again.Exit != exitConflict || !strings.Contains(output(again), "new chain id") {
		t.Errorf("publishing another genesis under a published chain id: exit %d, want %d and the reset rule\n%s", again.Exit, exitConflict, output(again))
	}
}

// TestMaintNetworkAnnounce_writesAnAnnouncementAndRefusesACreatedNetwork:
// announce writes manifest.json and release-root.json with no genesis, can be
// written again, refuses a production chain id with a faucet, and refuses a
// network that is already published with its genesis
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-maint-network-announce).
func TestMaintNetworkAnnounce_writesAnAnnouncementAndRefusesACreatedNetwork(t *testing.T) {
	t.Parallel()
	cli := isolated(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root.json")
	if err := os.WriteFile(root, []byte(`{"signed":{"_type":"root"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	networks := filepath.Join(dir, "networks")
	announce := []string{"maint", "network", "announce", "--dir", networks, "--name", "e2eann", "--chain-id", "orama-e2eann-stagenet-1",
		"--release-repo", "https://releases.example.org", "--release-root", root, "--channel", "nightly", "--faucet", "--seed", "seed1.e2eann.example.org"}
	res := cli.MustOK(t, announce...)
	if !strings.Contains(res.Stdout, "orama setup --create-network e2eann") {
		t.Errorf("announce printed:\n%s", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(networks, "e2eann", "genesis.json")); err == nil {
		t.Error("an announcement wrote a genesis")
	}
	cli.MustOK(t, announce...)

	production := run(t, cli, "maint", "network", "announce", "--dir", networks, "--name", "e2eprod", "--chain-id", "orama-1",
		"--release-repo", "https://releases.example.org", "--release-root", root, "--channel", "main", "--faucet")
	if production.Exit != exitUsage {
		t.Errorf("a production chain id with a faucet: exit %d, want %d\n%s", production.Exit, exitUsage, output(production))
	}

	genesis := filepath.Join(dir, "genesis.json")
	if err := os.WriteFile(genesis, []byte(`{"chain_id":"orama-e2eann-stagenet-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cli.MustOK(t, "maint", "network", "publish", "--dir", networks, "--name", "e2eann", "--chain-id", "orama-e2eann-stagenet-1", "--genesis", genesis)
	again := run(t, cli, announce...)
	if again.Exit != exitConflict || !strings.Contains(output(again), "already created") {
		t.Errorf("announcing a created network: exit %d, want %d and the reason\n%s", again.Exit, exitConflict, output(again))
	}
}
