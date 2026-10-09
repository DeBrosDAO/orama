package storagecmd

import (
	"fmt"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/storageclient"
	"github.com/spf13/cobra"
)

func init() {
	repair := &cobra.Command{
		Use:   "repair",
		Short: "Restore the replicas a deal lost, from the providers that still hold them",
		Long: `Rebuild every slot of a deal that the chain assigned to a new provider but that
no provider has accepted yet, using the repair seed.

For each such slot the command fetches an accepted replica from another
provider, checks it against its on-chain piece root, strips that slot's outer
layer, applies the new slot's layer, checks the result against the new slot's
on-chain root, and uploads it to the new provider. The plaintext is never
recovered. A repair seed that is not the deal's makes the result miss the
root, and nothing is uploaded.

A deal that names a repair delegate is repaired by the delegate while you are
away. Without one, the deal runs with fewer replicas until you run this.`,
		Args: cobra.NoArgs,
		RunE: runRepair,
	}
	repair.Flags().Uint64("deal-id", 0, "Deal id")
	repair.Flags().String("rpc", "", "oramad CometBFT RPC, for example http://127.0.0.1:31001")
	repair.Flags().String("repair-seed-file", "", "File holding the repair seed, hex, at least 32 bytes, mode 0600")
	repair.Flags().Duration("wait", storageclient.DefaultWait, "How long to wait for each new provider to read its assignment")
	for _, name := range []string{"deal-id", "rpc", "repair-seed-file"} {
		_ = repair.MarkFlagRequired(name)
	}
	Cmd.AddCommand(repair)
}

func runRepair(cmd *cobra.Command, _ []string) error {
	dealID, _ := cmd.Flags().GetUint64("deal-id")
	seed, err := secretFile(cmd, "repair-seed-file", "repair seed")
	if err != nil {
		return clierr.Usage("%v", err)
	}
	client, err := storageClient(cmd, true)
	if err != nil {
		return err
	}
	done, err := client.Repair(cmd.Context(), dealID, seed)
	for _, r := range done {
		fmt.Fprintf(cmd.OutOrStdout(), "restored slot %d from slot %d on %s\n", r.Slot, r.From, r.Provider)
	}
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if len(done) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "deal %d has no slot to restore\n", dealID)
	}
	return nil
}
