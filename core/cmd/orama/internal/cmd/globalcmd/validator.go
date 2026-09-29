package globalcmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/spf13/cobra"
)

// sealedFileMode is every sealed file the validator commands write.
const sealedFileMode = 0o600

var validatorCmd = &cobra.Command{
	Use:   "validator",
	Short: "Back up, move and manage this node's validator key",
}

var exportKeyFlags struct{ recipient, to string }

var exportKeyCmd = &cobra.Command{
	Use:   "export-key",
	Short: "Write priv_validator_key.json sealed to the operator's public key (run as root)",
	Long: `Seal priv_validator_key.json to --recipient, an X25519 public key (64 hex
characters), with the same ORBK seal as a namespace backup, and write it to --to.
The node never holds the private half, so it cannot open the file. --to must
not exist.

To restore the key on a new host, run 'orama global validator migrate prepare'
there, then 'orama global validator reseal' on the machine holding the private
key, then 'orama global validator migrate import' on the new host. Restore only
when the old host is gone: two hosts signing with one key is a double sign.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		recipient, err := globalnode.ParseX25519Hex(exportKeyFlags.recipient)
		if err != nil || exportKeyFlags.to == "" {
			return clierr.Usage("--recipient (64 hex characters) and --to are required")
		}
		if err := clierr.RequireRoot("reading the validator key"); err != nil {
			return err
		}
		sealed, err := globalnode.DefaultHost().ExportKey(recipient)
		if err != nil {
			return clierr.Failure("%v", err)
		}
		return writeNewFile(cmd, exportKeyFlags.to, sealed)
	},
}

var resealFlags struct{ from, identity, recipient, to string }

var resealCmd = &cobra.Command{
	Use:   "reseal",
	Short: "Turn a key backup into a migration bundle for a new host",
	Long: `Open a key backup from 'orama global validator export-key' with the operator's
X25519 private key (--identity-file, hex, mode 0600) and seal the key to the new
host's migration key (--recipient, printed by 'orama global validator migrate
prepare'). Run it on the machine that holds the private key, not on a node. The
bundle carries no sign state: nobody knows what a lost host last signed. Its
import therefore needs --old-host-destroyed and --floor-height <the network's
current height>, which become the new host's floor and state.`,
	Args: cobra.NoArgs,
	RunE: runReseal,
}

func init() {
	f := exportKeyCmd.Flags()
	f.StringVar(&exportKeyFlags.recipient, "recipient", "", "Operator X25519 public key, hex [required]")
	f.StringVar(&exportKeyFlags.to, "to", "", "File to write; must not exist [required]")
	r := resealCmd.Flags()
	r.StringVar(&resealFlags.from, "from", "", "Key backup from export-key [required]")
	r.StringVar(&resealFlags.identity, "identity-file", "", "File holding the operator X25519 private key, hex, mode 0600 [required]")
	r.StringVar(&resealFlags.recipient, "recipient", "", "The new host's migration key, hex [required]")
	r.StringVar(&resealFlags.to, "to", "", "Bundle file to write; must not exist [required]")
	validatorCmd.AddCommand(exportKeyCmd, resealCmd)
	Cmd.AddCommand(validatorCmd)
}

func runReseal(cmd *cobra.Command, _ []string) error {
	recipient, err := globalnode.ParseX25519Hex(resealFlags.recipient)
	if err != nil || resealFlags.from == "" || resealFlags.to == "" {
		return clierr.Usage("--from, --identity-file, --recipient (64 hex characters) and --to are required")
	}
	identity, err := readIdentityFile(resealFlags.identity)
	if err != nil {
		return err
	}
	backup, err := os.ReadFile(resealFlags.from)
	if err != nil {
		return clierr.Failure("read %s: %w", resealFlags.from, err)
	}
	bundle, err := globalnode.Reseal(identity, recipient, backup)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	return writeNewFile(cmd, resealFlags.to, bundle)
}

// readIdentityFile reads an X25519 private key. It is never taken on the
// command line, where ps and shell history keep it, and a file other users
// can read is refused.
func readIdentityFile(path string) (*[32]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, clierr.Usage("--identity-file: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, clierr.Usage("--identity-file %s is mode %o; chmod 600 it", path, info.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, clierr.Failure("read %s: %w", path, err)
	}
	return globalnode.ParseX25519Hex(string(body))
}

// writeNewFile writes data to path, which must not exist, without following
// a symlink planted there.
func writeNewFile(cmd *cobra.Command, path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, sealedFileMode)
	if err != nil {
		return clierr.Failure("create %s: %w", path, err)
	}
	if err := fillNewFile(f, data); err != nil {
		if rmErr := os.Remove(path); rmErr != nil {
			return clierr.Failure("%s: %v; the partial file could not be removed: %v", path, err, rmErr)
		}
		return clierr.Failure("%s: %v; the partial file was removed", path, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	return nil
}

// fillNewFile writes, syncs and closes f.
func fillNewFile(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}
