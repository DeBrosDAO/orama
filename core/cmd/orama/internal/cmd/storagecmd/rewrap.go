package storagecmd

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/storagefile"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use:   "rewrap",
		Short: "Rebuild one storage slot from another slot's ciphertext",
		Long: `Turn one sealed slot into another slot of the same deal.

The command uses the repair seed only. It does not recover the plaintext
and it does not upload the result.`,
		Args: cobra.NoArgs,
		RunE: runRewrap,
	}
	cmd.Flags().String("repair-seed", "", "Repair seed, hex, at least 32 bytes")
	cmd.Flags().String("nonce", "", "Deal nonce, 32 bytes hex")
	cmd.Flags().Uint32("from", 0, "Slot the input file belongs to")
	cmd.Flags().Uint32("to", 0, "Slot to write")
	cmd.Flags().String("in", "", "Source slot file")
	cmd.Flags().String("out", "", "Destination slot file")
	for _, name := range []string{"repair-seed", "nonce", "in", "out"} {
		_ = cmd.MarkFlagRequired(name)
	}
	Cmd.AddCommand(cmd)
}

func runRewrap(cmd *cobra.Command, _ []string) error {
	repair, nonce, err := repairKeys(cmd)
	if err != nil {
		return err
	}
	from, _ := cmd.Flags().GetUint32("from")
	to, _ := cmd.Flags().GetUint32("to")
	inPath, _ := cmd.Flags().GetString("in")
	outPath, _ := cmd.Flags().GetString("out")
	blob, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", inPath, err)
	}
	out, err := storagefile.Rewrap(repair, nonce, from, to, blob)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, out, 0o600)
}

func repairKeys(cmd *cobra.Command) (repair, nonce []byte, err error) {
	repairHex, _ := cmd.Flags().GetString("repair-seed")
	nonceHex, _ := cmd.Flags().GetString("nonce")
	repair, err = hex.DecodeString(repairHex)
	if err != nil || len(repair) < 32 {
		return nil, nil, fmt.Errorf("repair seed must be at least 32 bytes of hex")
	}
	nonce, err = hex.DecodeString(nonceHex)
	if err != nil || len(nonce) != storagefile.DealNonceLen {
		return nil, nil, fmt.Errorf("nonce must be %d bytes of hex", storagefile.DealNonceLen)
	}
	return repair, nonce, nil
}
