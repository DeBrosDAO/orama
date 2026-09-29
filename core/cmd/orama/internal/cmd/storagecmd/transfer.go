package storagecmd

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/storageclient"
	"github.com/DeBrosOfficial/network/pkg/storagefile"
	"github.com/spf13/cobra"
)

func init() {
	put := &cobra.Command{
		Use:   "put",
		Short: "Upload sealed slots to the providers a deal assigned",
		Long: `Upload the slot-N files written by seal to the providers the chain assigned.

The deal must already exist (orama storage create, with the roots seal printed).
Every file's piece root is checked against its slot on chain before any byte
is sent, so a wrong file or a wrong deal uploads nothing. The command waits
for each slot to be assigned and for its provider to accept the root. The
provider endpoint is the node's first http(s) endpoint in x/nodes.`,
		Args: cobra.NoArgs,
		RunE: runPut,
	}
	put.Flags().Uint64("deal-id", 0, "Deal id")
	put.Flags().String("dir", "", "Directory holding slot-N files from seal")
	put.Flags().String("rpc", "", "oramad CometBFT RPC, for example http://127.0.0.1:31001")
	put.Flags().Duration("wait", storageclient.DefaultWait, "How long to wait for assignment and acceptance")
	for _, name := range []string{"deal-id", "dir", "rpc"} {
		_ = put.MarkFlagRequired(name)
	}
	Cmd.AddCommand(put)

	get := &cobra.Command{
		Use:   "get",
		Short: "Fetch and open a private file from its providers",
		Long: `Fetch the first slot of a deal that a provider serves with the on-chain
piece root, strip its slot layer, and decrypt it. A wrong storage key or repair seed
fails and writes nothing.`,
		Args: cobra.NoArgs,
		RunE: runGet,
	}
	get.Flags().Uint64("deal-id", 0, "Deal id")
	get.Flags().String("rpc", "", "oramad CometBFT RPC, for example http://127.0.0.1:31001")
	get.Flags().String("storage-key-file", "", "File holding the orama-storage-v1 key from RootWallet (never the wallet seed), hex, exactly 32 bytes, mode 0600")
	get.Flags().String("repair-seed-file", "", "File holding the repair seed, hex, at least 32 bytes, mode 0600")
	get.Flags().String("out", "", "Plaintext output file")
	for _, name := range []string{"deal-id", "rpc", "storage-key-file", "repair-seed-file", "out"} {
		_ = get.MarkFlagRequired(name)
	}
	Cmd.AddCommand(get)
}

func storageClient(cmd *cobra.Command, wait bool) (*storageclient.Client, error) {
	rpc, _ := cmd.Flags().GetString("rpc")
	chain, err := storageclient.NewChain(rpc)
	if err != nil {
		return nil, clierr.Usage("%v", err)
	}
	d := storageclient.DefaultWait
	if wait {
		d, _ = cmd.Flags().GetDuration("wait")
	}
	return storageclient.New(chain, storageclient.PublicHTTPClient(transferTimeout), d)
}

func runPut(cmd *cobra.Command, _ []string) error {
	dealID, _ := cmd.Flags().GetUint64("deal-id")
	dir, _ := cmd.Flags().GetString("dir")
	client, err := storageClient(cmd, true)
	if err != nil {
		return err
	}
	var slots [][]byte
	for i := 0; ; i++ {
		body, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("slot-%d", i)))
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("read slot %d: %w", i, err)
		}
		slots = append(slots, body)
	}
	if len(slots) == 0 {
		return clierr.Usage("%s holds no slot-0 file", dir)
	}
	if err := client.Put(cmd.Context(), dealID, slots); err != nil {
		return clierr.Failure("%v", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "uploaded %d slots of deal %d\n", len(slots), dealID)
	return nil
}

func runGet(cmd *cobra.Command, _ []string) error {
	dealID, _ := cmd.Flags().GetUint64("deal-id")
	storageKey, repair, err := ownerAndRepairKeys(cmd)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	out, _ := cmd.Flags().GetString("out")
	client, err := storageClient(cmd, false)
	if err != nil {
		return err
	}
	plain, err := client.Get(cmd.Context(), dealID, storageKey, repair)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if err := os.WriteFile(out, plain, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	return nil
}

func ownerAndRepairKeys(cmd *cobra.Command) (storageKey, repair []byte, err error) {
	storageKey, err = storageKeyFile(cmd)
	if err != nil {
		return nil, nil, err
	}
	repair, err = secretFile(cmd, "repair-seed-file", "repair seed")
	if err != nil {
		return nil, nil, err
	}
	return storageKey, repair, nil
}

// secretFile reads a hex repair seed from the file named by flag.
func secretFile(cmd *cobra.Command, flag, what string) ([]byte, error) {
	secret, path, err := readHexSecret(cmd, flag)
	if err != nil {
		return nil, err
	}
	if len(secret) < minSeedLen {
		return nil, fmt.Errorf("%s in %s must be at least %d bytes of hex", what, path, minSeedLen)
	}
	return secret, nil
}

// storageKeyFile reads the owner's orama-storage-v1 key, exactly
// storagefile.StorageKeyLen bytes of hex. A longer value is most likely the
// wallet seed, which never belongs on this side, so it is refused.
func storageKeyFile(cmd *cobra.Command) ([]byte, error) {
	const flag = "storage-key-file"
	key, path, err := readHexSecret(cmd, flag)
	if err != nil {
		return nil, err
	}
	if len(key) != storagefile.StorageKeyLen {
		return nil, fmt.Errorf("the storage key in %s must be exactly %d bytes of hex, the orama-storage-v1 key from RootWallet, got %d bytes",
			path, storagefile.StorageKeyLen, len(key))
	}
	return key, nil
}

// readHexSecret reads a hex secret from the file named by flag. Secrets are
// not taken on the command line, where ps and shell history would keep them,
// and a file other users can read is refused.
func readHexSecret(cmd *cobra.Command, flag string) (secret []byte, path string, err error) {
	path, _ = cmd.Flags().GetString(flag)
	info, err := os.Stat(path)
	if err != nil {
		return nil, path, fmt.Errorf("--%s: %w", flag, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, path, fmt.Errorf("--%s %s is mode %o; chmod 600 it", flag, path, info.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("--%s: %w", flag, err)
	}
	secret, err = hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, path, fmt.Errorf("--%s %s is not hex: %w", flag, path, err)
	}
	return secret, path, nil
}

// minSeedLen is storagefile's minimum repair-seed length.
const minSeedLen = 32

// transferTimeout bounds one storage put or get.
const transferTimeout = 30 * time.Minute
