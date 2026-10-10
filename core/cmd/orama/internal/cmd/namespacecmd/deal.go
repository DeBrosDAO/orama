package namespacecmd

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/storagecmd"
	"github.com/spf13/cobra"
)

// A namespace backup is already sealed to the owner's X25519 key, which the
// cluster cannot open. Keeping it in a private storage deal adds the deal's
// own slot layer under the owner's storage key, so the providers hold one
// distinct ciphertext per replica and the chain commits to each piece root.
// The deal itself is opened and uploaded with `orama storage create` and
// `orama storage put`; this file only prepares the slots for them and reads a
// deal back for a restore.

// dealSealFlags are the flags backup takes to leave slot files for a deal.
type dealSealFlags struct {
	dir      string
	nonce    string
	replicas int
	keys     storagecmd.Keys
}

// dealFetchFlags are the flags restore takes to read a backup from a deal.
type dealFetchFlags struct {
	dealID uint64
	rpc    string
	keys   storagecmd.Keys
}

// fetchDeal reads a private deal from its providers; tests replace it.
var fetchDeal = storagecmd.FetchPrivate

func addDealSealFlags(c *cobra.Command) {
	f := c.Flags()
	f.String("deal-dir", "", "also seal the backup into one slot-N file per replica of a private storage deal, in this directory")
	f.String("deal-nonce", "", "the deal's 32-byte nonce, hex; the same value goes to 'orama storage create --nonce' (needed with --deal-dir)")
	f.Int("deal-replicas", 3, "number of slots to write; the same value goes to 'orama storage create --replicas'")
	f.String("storage-key-file", "", "file holding your orama-storage-v1 key, hex, mode 0600 (needed with --deal-dir)")
	f.String("repair-seed-file", "", "file holding your repair seed, hex, mode 0600 (needed with --deal-dir)")
}

func addDealFetchFlags(c *cobra.Command) {
	f := c.Flags()
	f.Uint64("from-deal", 0, "read the sealed backup from this private storage deal instead of --in")
	f.String("rpc", "", "oramad CometBFT RPC, for example http://127.0.0.1:31001 (needed with --from-deal)")
	f.String("storage-key-file", "", "file holding your orama-storage-v1 key, hex, mode 0600 (needed with --from-deal)")
	f.String("repair-seed-file", "", "file holding your repair seed, hex, mode 0600 (needed with --from-deal)")
}

func dealSealFlagsFrom(cmd *cobra.Command) dealSealFlags {
	var d dealSealFlags
	d.dir, _ = cmd.Flags().GetString("deal-dir")
	d.nonce, _ = cmd.Flags().GetString("deal-nonce")
	d.replicas, _ = cmd.Flags().GetInt("deal-replicas")
	d.keys.StorageKeyFile, _ = cmd.Flags().GetString("storage-key-file")
	d.keys.RepairSeedFile, _ = cmd.Flags().GetString("repair-seed-file")
	return d
}

func dealFetchFlagsFrom(cmd *cobra.Command) dealFetchFlags {
	var d dealFetchFlags
	d.dealID, _ = cmd.Flags().GetUint64("from-deal")
	d.rpc, _ = cmd.Flags().GetString("rpc")
	d.keys.StorageKeyFile, _ = cmd.Flags().GetString("storage-key-file")
	d.keys.RepairSeedFile, _ = cmd.Flags().GetString("repair-seed-file")
	return d
}

// validate refuses a --deal-dir that lacks what sealing needs before the
// backup is requested, so no backup is taken and then cannot be stored.
func (d dealSealFlags) validate() error {
	if d.dir == "" {
		return nil
	}
	if d.nonce == "" || d.keys.StorageKeyFile == "" || d.keys.RepairSeedFile == "" {
		return fmt.Errorf("--deal-dir needs --deal-nonce, --storage-key-file and --repair-seed-file")
	}
	return nil
}

// sealForDeal seals the backup into slot files and prints what
// `orama storage create` needs: each slot's root and size.
func sealForDeal(out io.Writer, blob []byte, d dealSealFlags) error {
	slots, err := storagecmd.SealSlots(d.keys, d.nonce, d.replicas, blob)
	if err != nil {
		return fmt.Errorf("seal the backup for a storage deal: %w", err)
	}
	if err := storagecmd.WriteSlots(d.dir, slots, out); err != nil {
		return err
	}
	pieces := make([]string, 0, len(slots))
	for _, s := range slots {
		pieces = append(pieces, fmt.Sprintf("--piece %s:%d", hex.EncodeToString(s.Root), len(s.Bytes)))
	}
	fmt.Fprintf(out, "Open the deal with: orama storage create --class private --nonce %s --replicas %d %s\n",
		d.nonce, len(slots), strings.Join(pieces, " "))
	fmt.Fprintf(out, "Then upload the slots with: orama storage put --deal-id <id> --dir %s --rpc <oramad RPC>\n", d.dir)
	return nil
}

// restoreBlob is the sealed backup a restore opens: the file at --in, or the
// deal named by --from-deal. Exactly one of the two must be given.
func restoreBlob(ctx context.Context, inPath string, d dealFetchFlags) ([]byte, error) {
	switch {
	case inPath != "" && d.dealID != 0:
		return nil, fmt.Errorf("give --in or --from-deal, not both")
	case inPath != "":
		blob, err := os.ReadFile(inPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", inPath, err)
		}
		return blob, nil
	case d.dealID == 0:
		return nil, fmt.Errorf("give the sealed backup with --in, or the deal that holds it with --from-deal")
	case d.rpc == "":
		return nil, fmt.Errorf("--from-deal needs --rpc")
	}
	blob, err := fetchDeal(ctx, d.rpc, d.dealID, d.keys)
	if err != nil {
		return nil, fmt.Errorf("read the backup from deal %d: %w", d.dealID, err)
	}
	return blob, nil
}
