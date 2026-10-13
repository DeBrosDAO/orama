package globalcmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/spf13/cobra"
)

func runAuthorityKeysWith(t *testing.T, nickname, address string) (err error, installed bool) {
	t.Helper()
	saved, savedInstall := authorityKeysFlags, ensureTorPackage
	defer func() { authorityKeysFlags, ensureTorPackage = saved, savedInstall }()
	authorityKeysFlags.nickname, authorityKeysFlags.address = nickname, address
	authorityKeysFlags.dir = filepath.Join(t.TempDir(), "keys")
	ensureTorPackage = func(io.Writer) error { installed = true; return nil }
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return runAuthorityKeys(cmd, nil), installed
}

func TestRunAuthorityKeys_refusesBeforeInstallingAnything(t *testing.T) {
	cases := map[string][2]string{
		"no nickname":             {"", "57.129.166.16"},
		"no address":              {"OramaAuth01", ""},
		"a private address":       {"OramaAuth01", "10.0.0.4"},
		"a hostname":              {"OramaAuth01", "ns1.example.org"},
		"a nickname with a space": {"Orama Auth", "57.129.166.16"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err, installed := runAuthorityKeysWith(t, c[0], c[1])
			if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
				t.Fatalf("err = %v, want a usage error", err)
			}
			if installed {
				t.Error("the Tor package was installed for a request that was going to be refused")
			}
		})
	}
}

func TestRunAuthorityKeys_needsRootBeforeItTouchesTheMachine(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: the refusal for a caller that is not root cannot be seen")
	}
	err, installed := runAuthorityKeysWith(t, "OramaAuth01", "57.129.166.16")
	if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v, want a usage error saying to run as root", err)
	}
	if installed {
		t.Error("the Tor package was installed by a caller that is not root")
	}
}

func TestAuthorityKeysCommand_isUnderMaintTor(t *testing.T) {
	found, _, err := maintTorCmd.Find([]string{"authority-keys"})
	if err != nil || found != authorityKeysCmd {
		t.Fatalf("maint global tor authority-keys not found: %v", err)
	}
	for _, flag := range []string{"nickname", "address", "dir"} {
		if authorityKeysCmd.Flags().Lookup(flag) == nil {
			t.Errorf("no --%s flag", flag)
		}
	}
}
