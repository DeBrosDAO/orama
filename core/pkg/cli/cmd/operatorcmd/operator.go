package operatorcmd

import (
	"encoding/json"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"github.com/DeBrosOfficial/network/pkg/cli/shared"
	"github.com/spf13/cobra"
)

// Cmd groups the operations that belong to whoever runs the cluster rather than
// to a tenant.
var Cmd = &cobra.Command{
	Use:   "operator",
	Short: "Operate the cluster",
	Long: `Commands for the wallets on the cluster's operator list.

Every one of them needs the admin grant and a wallet on that list; a namespace's
own admin key is not enough.`,
}

var rotateSigningKeyCmd = &cobra.Command{
	Use:   "rotate-signing-key",
	Short: "Replace the key this gateway signs tokens with",
	Long: `Generate a new signing key for the gateway, publish it, and start signing
with it.

Nobody is signed out. The outgoing key keeps verifying the tokens it already
signed until they expire on their own, so both keys are accepted for one
access-token lifetime and then the old one stops.

The key used to be derived from the cluster secret, which meant there was
nothing to rotate to: changing it meant changing the cluster secret, which
invalidates every token in the cluster at once.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := shared.Request("POST", "/v1/operator/rotate-signing-key", nil)
		if err != nil {
			return err
		}

		var resp struct {
			KID                 string `json:"kid"`
			PreviousKID         string `json:"previous_kid"`
			Namespace           string `json:"namespace"`
			PreviousAcceptedFor string `json:"previous_accepted_for"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return clierr.Failure("could not parse the gateway's reply: %w", err)
		}
		if resp.KID == "" {
			return clierr.Failure("the gateway reported no new key")
		}

		fmt.Printf("Signing key rotated.\n\n")
		fmt.Printf("  New key:      %s\n", resp.KID)
		fmt.Printf("  Previous key: %s\n", resp.PreviousKID)
		if resp.Namespace != "" {
			fmt.Printf("  Signs for:    %s\n", resp.Namespace)
		} else {
			fmt.Printf("  Signs for:    every namespace (this is the index gateway)\n")
		}
		fmt.Printf("\nThe previous key keeps verifying tokens it already signed for %s.\n", resp.PreviousAcceptedFor)
		return nil
	},
}

func init() {
	Cmd.AddCommand(rotateSigningKeyCmd)
}
