package namespacecmd

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/spf13/cobra"
)

var backupSealCmd = &cobra.Command{
	Use:   "backup-seal",
	Short: "Encrypt a backup file to an X25519 public key",
	Long: `Encrypt a file to the owner's backup public key.

The cluster holds only that public key. It cannot decrypt the file.
The full namespace restore (RQLite, pins, and secret re-wrap) is not this command.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBackup(cmd, true)
	},
}

var backupOpenCmd = &cobra.Command{
	Use:   "backup-open",
	Short: "Decrypt a backup file with an X25519 private key",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBackup(cmd, false)
	},
}

func runBackup(cmd *cobra.Command, seal bool) error {
	keyHex, _ := cmd.Flags().GetString("key")
	inPath, _ := cmd.Flags().GetString("in")
	outPath, _ := cmd.Flags().GetString("out")
	raw, err := hex.DecodeString(keyHex)
	if err != nil || len(raw) != 32 {
		return fmt.Errorf("key must be 64 hex characters")
	}
	var key [32]byte
	copy(key[:], raw)
	body, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", inPath, err)
	}
	var out []byte
	if seal {
		out, err = nsbackup.Seal(&key, body)
	} else {
		out, err = nsbackup.Open(&key, body)
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, out, 0600); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}

func init() {
	for _, c := range []*cobra.Command{backupSealCmd, backupOpenCmd} {
		c.Flags().String("key", "", "32-byte X25519 key, hex (public for seal, private for open)")
		c.Flags().String("in", "", "input file")
		c.Flags().String("out", "", "output file")
		_ = c.MarkFlagRequired("key")
		_ = c.MarkFlagRequired("in")
		_ = c.MarkFlagRequired("out")
	}
}
