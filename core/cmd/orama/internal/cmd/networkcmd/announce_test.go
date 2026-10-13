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

func announce(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "orama", SilenceUsage: true, SilenceErrors: true}
	printer.Register(root)
	maint := &cobra.Command{Use: "maint"}
	group := &cobra.Command{Use: "network"}
	group.AddCommand(newAnnounceCmd())
	maint.AddCommand(group)
	root.AddCommand(maint)
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"maint", "network", "announce"}, args...))
	err := root.Execute()
	return out.String(), err
}

func announceArgs(t *testing.T, tmp string) []string {
	t.Helper()
	return []string{
		"--dir", filepath.Join(tmp, "networks"), "--name", "pubnet", "--chain-id", "orama-pubnet-stagenet-1",
		"--release-repo", "https://releases.example.org", "--release-root", writeFile(t, tmp, "root.json", pubRoot),
		"--channel", "nightly", "--faucet", "--seed", "seed1.pubnet.example.org", "--seed", "seed2.pubnet.example.org",
	}
}

func TestAnnounce_writesAnAnnouncementTheRegistryReads(t *testing.T) {
	tmp := t.TempDir()
	out, err := announce(t, announceArgs(t, tmp)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Announced network pubnet", "orama-pubnet-stagenet-1", "sync-networks", "orama setup --create-network pubnet"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	dir := filepath.Join(tmp, "networks", "pubnet")
	data, err := os.ReadFile(filepath.Join(dir, netregistry.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	m, err := netregistry.ParseManifest(data)
	if err != nil || !m.Announced() || !m.Faucet || len(m.Seeds) != 2 || m.ReleaseRootSHA256 != netregistry.Digest([]byte(pubRoot)) || m.MinVersion == "" {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(dir, netregistry.GenesisFile)); err == nil {
		t.Error("an announcement writes no genesis")
	}
}

func TestAnnounce_aCreatedNetworkIsAConflict(t *testing.T) {
	tmp := t.TempDir()
	if _, err := publish(t, firstPublishArgs(t, tmp)...); err != nil {
		t.Fatal(err)
	}
	_, err := announce(t, announceArgs(t, tmp)...)
	if clierr.CodeOf(err) != clierr.CodeConflict {
		t.Fatalf("error = %v (code %d), want a conflict", err, clierr.CodeOf(err))
	}
}

func TestAnnounce_refusals(t *testing.T) {
	tmp := t.TempDir()
	good := announceArgs(t, tmp)
	with := func(flag, value string) []string {
		return append(append([]string{}, good...), flag, value)
	}
	for name, args := range map[string][]string{
		"no flags":                              {},
		"a release root not found":              with("--release-root", filepath.Join(tmp, "absent.json")),
		"a production chain id with the faucet": with("--chain-id", "orama-1"),
		"a bad channel":                         with("--channel", "beta"),
		"a release repo over http":              with("--release-repo", "http://releases.example.org"),
		"a seed that is an IP":                  with("--seed", "203.0.113.9"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := announce(t, args...)
			if err == nil {
				t.Fatal("announce accepted it")
			}
			if name != "no flags" && clierr.CodeOf(err) != clierr.CodeUsage {
				t.Errorf("code = %d, want a usage error: %v", clierr.CodeOf(err), err)
			}
		})
	}
}
