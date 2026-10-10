package networkcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

const (
	pubGenesis = `{"chain_id":"orama-pubnet-1"}`
	pubRoot    = `{"signed":{"_type":"root"}}`
)

func publish(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "orama", SilenceUsage: true, SilenceErrors: true}
	printer.Register(root)
	maint := &cobra.Command{Use: "maint"}
	group := &cobra.Command{Use: "network"}
	group.AddCommand(newPublishCmd())
	maint.AddCommand(group)
	root.AddCommand(maint)
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"maint", "network", "publish"}, args...))
	err := root.Execute()
	return out.String(), err
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func firstPublishArgs(t *testing.T, tmp string) []string {
	t.Helper()
	return []string{
		"--dir", filepath.Join(tmp, "networks"), "--name", "pubnet", "--chain-id", "orama-pubnet-1",
		"--genesis", writeFile(t, tmp, "genesis.json", pubGenesis),
		"--release-root", writeFile(t, tmp, "root.json", pubRoot),
		"--seeds", "seed1.pubnet.example.org,seed2.pubnet.example.org",
		"--channel", "nightly", "--min-version", "0.3.0", "--release-repo", "https://releases.example.org/", "--faucet",
	}
}

func TestPublish_firstPublishWritesAManifestTheRegistryReads(t *testing.T) {
	tmp := t.TempDir()
	out, err := publish(t, firstPublishArgs(t, tmp)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sync-networks") || !strings.Contains(out, "orama-pubnet-1") {
		t.Errorf("output does not name the chain and the next step:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "networks", "pubnet", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := netregistry.ParseManifest(data)
	if err != nil || !m.Faucet || len(m.Seeds) != 2 || m.GenesisSHA256 != netregistry.Digest([]byte(pubGenesis)) {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
}

func TestPublish_aResetKeepsTheRestAndAChangedGenesisIsAConflict(t *testing.T) {
	tmp := t.TempDir()
	if _, err := publish(t, firstPublishArgs(t, tmp)...); err != nil {
		t.Fatal(err)
	}
	next := writeFile(t, tmp, "genesis2.json", `{"chain_id":"orama-pubnet-2"}`)
	if _, err := publish(t, "--dir", filepath.Join(tmp, "networks"), "--name", "pubnet", "--chain-id", "orama-pubnet-2", "--genesis", next); err != nil {
		t.Fatalf("a reset with a new chain id: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(tmp, "networks", "pubnet", "manifest.json"))
	m, _ := netregistry.ParseManifest(data)
	if m.ChainID != "orama-pubnet-2" || !m.Faucet || len(m.Seeds) != 2 {
		t.Errorf("the reset lost the rest of the manifest: %+v", m)
	}

	changed := writeFile(t, tmp, "changed.json", `{"chain_id":"orama-pubnet-2","x":1}`)
	_, err := publish(t, "--dir", filepath.Join(tmp, "networks"), "--name", "pubnet", "--chain-id", "orama-pubnet-2", "--genesis", changed)
	if clierr.CodeOf(err) != clierr.CodeConflict {
		t.Fatalf("error = %v (code %d), want a conflict", err, clierr.CodeOf(err))
	}
}

func TestPublish_missingInputs(t *testing.T) {
	tmp := t.TempDir()
	for name, args := range map[string][]string{
		"no flags":           {},
		"no genesis flag":    {"--name", "n", "--chain-id", "c"},
		"genesis not found":  {"--name", "pubnet", "--chain-id", "orama-pubnet-1", "--genesis", filepath.Join(tmp, "absent.json")},
		"root not found":     {"--name", "pubnet", "--chain-id", "orama-pubnet-1", "--genesis", writeFile(t, tmp, "g.json", pubGenesis), "--release-root", filepath.Join(tmp, "absent.json")},
		"first publish bare": {"--dir", filepath.Join(tmp, "n"), "--name", "pubnet", "--chain-id", "orama-pubnet-1", "--genesis", writeFile(t, tmp, "g2.json", pubGenesis)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := publish(t, args...); err == nil {
				t.Fatal("publish accepted it")
			}
		})
	}
}
