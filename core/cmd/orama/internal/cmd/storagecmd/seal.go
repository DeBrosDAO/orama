package storagecmd

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/storagefile"
	"github.com/spf13/cobra"
)

func init() {
	seal := &cobra.Command{
		Use:   "seal",
		Short: "Seal a file into one ciphertext per storage slot",
		Long: `Seal a private file before a storage deal.

The file key is wrapped under the owner's orama-storage-v1 key from RootWallet. Each slot gets a different
ciphertext. The command writes slot-N files and prints each piece root.
It does not upload the bytes and it does not submit a deal.`,
		Args: cobra.NoArgs,
		RunE: runSeal,
	}
	seal.Flags().String("storage-key-file", "", "File holding the orama-storage-v1 key from RootWallet (never the wallet seed), hex, exactly 32 bytes, mode 0600")
	seal.Flags().String("repair-seed-file", "", "File holding the repair seed, hex, at least 32 bytes, mode 0600")
	seal.Flags().String("nonce", "", "Deal nonce, 32 bytes hex")
	seal.Flags().Int("replicas", 3, "Number of slots, 1 to 32")
	seal.Flags().String("in", "", "Plaintext file")
	seal.Flags().String("out-dir", "", "Directory for slot-N files")
	for _, name := range []string{"storage-key-file", "repair-seed-file", "nonce", "in", "out-dir"} {
		_ = seal.MarkFlagRequired(name)
	}
	Cmd.AddCommand(seal)

	open := &cobra.Command{
		Use:   "open",
		Short: "Open one sealed storage slot",
		Long: `Open one slot file written by seal.

A wrong storage key, repair seed, or slot fails and writes nothing.`,
		Args: cobra.NoArgs,
		RunE: runOpen,
	}
	open.Flags().String("storage-key-file", "", "File holding the orama-storage-v1 key from RootWallet (never the wallet seed), hex, exactly 32 bytes, mode 0600")
	open.Flags().String("repair-seed-file", "", "File holding the repair seed, hex, at least 32 bytes, mode 0600")
	open.Flags().String("nonce", "", "Deal nonce, 32 bytes hex")
	open.Flags().Uint32("slot", 0, "Slot index")
	open.Flags().String("in", "", "Sealed slot file")
	open.Flags().String("out", "", "Plaintext output file")
	for _, name := range []string{"storage-key-file", "repair-seed-file", "nonce", "in", "out"} {
		_ = open.MarkFlagRequired(name)
	}
	Cmd.AddCommand(open)
}

func runSeal(cmd *cobra.Command, _ []string) error {
	replicas, _ := cmd.Flags().GetInt("replicas")
	if replicas < storagefile.MinReplicas || replicas > storagefile.MaxReplicas {
		return clierr.Usage("--replicas must be from %d to %d, got %d", storagefile.MinReplicas, storagefile.MaxReplicas, replicas)
	}
	storageKey, repair, nonce, err := sealKeys(cmd)
	if err != nil {
		return err
	}
	inPath, _ := cmd.Flags().GetString("in")
	outDir, _ := cmd.Flags().GetString("out-dir")
	plain, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", inPath, err)
	}
	slots, err := storagefile.Prepare(storageKey, repair, nonce, replicas, plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return err
	}
	for _, slot := range slots {
		path := filepath.Join(outDir, fmt.Sprintf("slot-%d", slot.Index))
		if err := os.WriteFile(path, slot.Bytes, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "slot %d root %s\n", slot.Index, hex.EncodeToString(slot.Root))
	}
	return nil
}

func runOpen(cmd *cobra.Command, _ []string) error {
	storageKey, repair, nonce, err := sealKeys(cmd)
	if err != nil {
		return err
	}
	slot, _ := cmd.Flags().GetUint32("slot")
	inPath, _ := cmd.Flags().GetString("in")
	outPath, _ := cmd.Flags().GetString("out")
	blob, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", inPath, err)
	}
	plain, err := storagefile.Open(storageKey, repair, nonce, slot, blob)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, plain, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}

func sealKeys(cmd *cobra.Command) (storageKey, repair, nonce []byte, err error) {
	storageKey, repair, err = ownerAndRepairKeys(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	nonce, err = nonceFlag(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	return storageKey, repair, nonce, nil
}
