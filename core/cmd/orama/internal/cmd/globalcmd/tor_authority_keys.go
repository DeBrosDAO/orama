package globalcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
	"github.com/DeBrosOfficial/network/pkg/tornet"
	"github.com/spf13/cobra"
)

// authorityKeysDirMode is the mode of the keys home: root's, nobody else enters.
const authorityKeysDirMode = 0o700

var authorityKeysFlags struct{ nickname, address, dir string }

// ensureTorPackage installs the Tor Project's tor package (which ships
// tor-gencert) unless it is already there; progress goes to w. A seam for tests.
var ensureTorPackage = func(w io.Writer) error {
	return installers.NewTorInstaller(runtime.GOARCH, w).EnsureInstalled()
}

var authorityKeysCmd = &cobra.Command{
	Use:   "authority-keys",
	Short: "Make this machine's directory-authority keys, or read back the ones it has (run as root)",
	Long: `Make the keys of ONE directory authority on this machine, which will run it: the
relay identity (tor --list-fingerprint) and the authority identity key, signing key and
12-month certificate (tor-gencert), the same programs and key handling as
'orama maint global tor ceremony'. The tor package of the Tor Project's repository
is installed first if it is missing.

Below --dir (default ` + constants.GlobalTorAuthorityKeysDir + `, root only) it writes
  offline/<nickname>/authority_identity_key   the identity key, encrypted, mode 0400
  offline/<nickname>/passphrase               its passphrase, generated here, mode 0400
  deploy/<nickname>/keys/                     the signing key, the certificate and the
                                              relay identity: what 'orama global
                                              install --services dirauth' reads, with
                                              --tor-authority-keys deploy/<nickname>
Nothing secret is printed or leaves the machine. The identity key is never in the
bundle and never in a tor DataDirectory.

It prints the public identity as one line of JSON: the nickname, address, ports, v3
identity, relay fingerprint, ed25519 identity, the certificate's expiry and whether
this run made the keys. Running it again reads the keys back and checks them; keys
are never made over keys, and a directory that holds part of a set is an error.

This is how 'orama setup --create-network' makes the authorities of a new network's own Tor
network, on a test network. The passphrase beside the key is that arrangement's trade-off:
a production network runs the ceremony on an offline machine instead.`,
	Args: cobra.NoArgs,
	RunE: runAuthorityKeys,
}

func runAuthorityKeys(cmd *cobra.Command, _ []string) error {
	f := authorityKeysFlags
	if f.nickname == "" || f.address == "" {
		return clierr.Usage("--nickname and --address are required")
	}
	spec := tornet.AuthoritySpec{Nickname: f.nickname, Address: f.address, ORPort: constants.GlobalTorORPort, DirPort: constants.GlobalTorDirPort}
	if err := spec.Validate(); err != nil {
		return clierr.Usage("%v", err)
	}
	if err := clierr.RequireRoot("making an authority's keys"); err != nil {
		return err
	}
	if err := ensureTorPackage(cmd.ErrOrStderr()); err != nil {
		return clierr.Failure("install the Tor package: %v", err)
	}
	if err := os.MkdirAll(f.dir, authorityKeysDirMode); err != nil {
		return clierr.Failure("create %s: %v", f.dir, err)
	}
	if err := os.Chmod(f.dir, authorityKeysDirMode); err != nil {
		return clierr.Failure("make %s private: %v", f.dir, err)
	}
	keys, err := tornet.EnsureAuthorityKeys(cmd.Context(), runProgram, tornet.AuthorityKeysRequest{Spec: spec, Dir: f.dir})
	if err != nil {
		return clierr.Failure("%v", err)
	}
	doc, err := json.Marshal(keys)
	if err != nil {
		return clierr.Failure("encode the authority keys: %v", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", doc)
	return nil
}

func init() {
	f := authorityKeysCmd.Flags()
	f.StringVar(&authorityKeysFlags.nickname, "nickname", "", "The authority's nickname, 1 to 19 letters and digits [required]")
	f.StringVar(&authorityKeysFlags.address, "address", "", "The authority's public IPv4 address [required]")
	f.StringVar(&authorityKeysFlags.dir, "dir", constants.GlobalTorAuthorityKeysDir, "The keys home; only root may enter it")
	maintTorCmd.AddCommand(cmdmeta.MarkNodeLocal(authorityKeysCmd))
}
