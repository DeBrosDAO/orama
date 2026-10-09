package operatorcmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
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

var rotateSecretsRotate bool

var rotateSecretsCmd = &cobra.Command{
	Use:   "rotate-secrets",
	Short: "Re-encrypt stored secrets, optionally under a new encryption root",
	Long: `Rewrite function secrets, push tokens, TURN secrets, deployment
environments and agent tokens onto the versioned envelope (enc:v1:<id>:).
A deployment's environment is written as enc:v2:<id>:, sealed to its namespace
and deployment id, so a copy of the ciphertext in another deployment's row does
not open. This is also what turns on bound writes: until it has run, gateways
keep writing deployment environments in the envelope an older gateway reads.

Without --rotate the IKM does not change: leftover plaintext and the legacy
enc: form are rewritten so a captured snapshot of the old format is no longer
the live one, and Decrypt can fail closed.

With --rotate a new encryption root is generated. Existing ciphertext is
re-encrypted under it. A disk that holds only the previous root cannot open
the new rows. IPFS-Cluster and the mesh bearer are not touched.

Do not run this until every gateway is on a binary that can read enc:v2:.
The walker is idempotent; if it is interrupted, run it again.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]bool{"rotate": rotateSecretsRotate}
		raw, err := shared.Request("POST", "/v1/operator/rotate-secrets", body)
		if err != nil {
			return err
		}
		var resp struct {
			KeyID      string `json:"key_id"`
			PreviousID string `json:"previous_id"`
			Rotated    bool   `json:"rotated"`
			Index      struct {
				Scanned  int      `json:"scanned"`
				Rewrote  int      `json:"rewrote"`
				Skipped  int      `json:"skipped"`
				Failures []string `json:"failures"`
			} `json:"index"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return clierr.Failure("could not parse the gateway's reply: %w", err)
		}
		fmt.Printf("Stored secrets rewritten.\n\n")
		fmt.Printf("  Key id:     %s\n", resp.KeyID)
		if resp.PreviousID != "" {
			fmt.Printf("  Previous:   %s\n", resp.PreviousID)
		}
		if resp.Rotated {
			fmt.Printf("  IKM:        replaced (a captured previous root cannot open new rows)\n")
		} else {
			fmt.Printf("  IKM:        unchanged (format rewrite only)\n")
		}
		fmt.Printf("  Index:      scanned %d, rewrote %d, skipped %d\n",
			resp.Index.Scanned, resp.Index.Rewrote, resp.Index.Skipped)
		if len(resp.Index.Failures) > 0 {
			fmt.Printf("  Failures:   %d\n", len(resp.Index.Failures))
		}
		return nil
	},
}

var listOperatorsCmd = &cobra.Command{
	Use:   "list",
	Short: "List the wallets that operate this cluster",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := shared.Request("GET", "/v1/operator/operators", nil)
		if err != nil {
			return err
		}
		var resp struct {
			Operators []struct {
				Wallet  string `json:"wallet"`
				AddedBy string `json:"added_by"`
				AddedAt string `json:"added_at"`
			} `json:"operators"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return clierr.Failure("could not parse the gateway's reply: %w", err)
		}
		if len(resp.Operators) == 0 {
			fmt.Println("No operators.")
			return nil
		}
		for _, op := range resp.Operators {
			fmt.Printf("%s  added by %s\n", op.Wallet, op.AddedBy)
		}
		return nil
	},
}

var addOperatorCmd = &cobra.Command{
	Use:   "add <wallet>",
	Short: "Let another wallet operate this cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wallet := strings.TrimSpace(args[0])
		raw, err := shared.Request("POST", "/v1/operator/operators", map[string]string{"wallet": wallet})
		if err != nil {
			return err
		}
		var resp struct {
			Wallet string `json:"wallet"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return clierr.Failure("could not parse the gateway's reply: %w", err)
		}
		fmt.Printf("Operator added: %s\n", resp.Wallet)
		return nil
	},
}

var removeOperatorCmd = &cobra.Command{
	Use:   "remove <wallet>",
	Short: "Take a wallet off this cluster's operator list",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wallet := url.PathEscape(strings.TrimSpace(args[0]))
		raw, err := shared.Request("DELETE", "/v1/operator/operators/"+wallet, nil)
		if err != nil {
			return err
		}
		var resp struct {
			Wallet string `json:"wallet"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return clierr.Failure("could not parse the gateway's reply: %w", err)
		}
		fmt.Printf("Operator removed: %s\n", resp.Wallet)
		return nil
	},
}

func init() {
	rotateSecretsCmd.Flags().BoolVar(&rotateSecretsRotate, "rotate", false,
		"Generate a new encryption root and re-encrypt under it")
	Cmd.AddCommand(listOperatorsCmd)
	Cmd.AddCommand(addOperatorCmd)
	Cmd.AddCommand(removeOperatorCmd)
	Cmd.AddCommand(rotateSigningKeyCmd)
	Cmd.AddCommand(rotateSecretsCmd)
}
