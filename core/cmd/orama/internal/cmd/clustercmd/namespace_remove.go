package clustercmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
	"github.com/spf13/cobra"
)

// removeTimeout bounds the removal call. The gateway answers after the whole
// teardown — the cluster stopped on every node, content released, rows
// deleted — which takes minutes; the CLI's usual 30s bound gave up while the
// gateway carried on, and reported a removal that then completed as a failure.
const removeTimeout = 10 * time.Minute

var (
	removeReason string
	removeForce  bool
)

var namespaceCmd = &cobra.Command{
	Use:   "namespace",
	Short: "Operator actions on a namespace",
}

var namespaceRemoveCmd = &cobra.Command{
	Use:   "remove <namespace>",
	Short: "Remove a namespace whose owner can no longer delete it",
	Long: `Remove a namespace and everything in it: its cluster on every node, its
deployments, its stored content (unless another namespace holds the same), its
keys and its grants.

The owner deletes a namespace with 'orama namespace delete'. This is for a
namespace whose owner cannot: the owner's wallet is lost, or it belonged to a
test run's throwaway wallet. Such a namespace keeps its port blocks and
processes on three nodes until an operator removes it.

It needs the operator grant and a wallet on the operator list. --reason is
required and is written to the audit trail with your wallet
(namespace.operator_remove). You are asked to type the namespace name unless
--force is given.`,
	Args: cobra.ExactArgs(1),
	RunE: removeNamespace,
}

func init() {
	namespaceRemoveCmd.Flags().StringVar(&removeReason, "reason", "", "Why the namespace is removed; recorded in the audit trail [required]")
	namespaceRemoveCmd.Flags().BoolVar(&removeForce, "force", false, "Do not ask to type the namespace name")
	_ = namespaceRemoveCmd.MarkFlagRequired("reason")
	namespaceCmd.AddCommand(namespaceRemoveCmd)
	Cmd.AddCommand(namespaceCmd)
}

func removeNamespace(cmd *cobra.Command, args []string) error {
	name := strings.ToLower(strings.TrimSpace(args[0]))
	reason := strings.TrimSpace(removeReason)
	if name == "" {
		return clierr.Usage("namespace is required")
	}
	if reason == "" {
		return clierr.Usage("--reason is required: it is recorded in the audit trail")
	}
	if !removeForce && !shared.ConfirmExact(
		fmt.Sprintf("This removes namespace %s and everything in it. Type its name to confirm", name), name) {
		return clierr.Aborted("cancelled: what you typed did not match %q", name)
	}
	gatewayURL, err := shared.GetAPIURL()
	if err != nil {
		return err
	}
	token, err := shared.GetAuthToken()
	if err != nil {
		return err
	}
	raw, _, err := shared.RequestWith(&http.Client{Timeout: removeTimeout}, gatewayURL, token,
		"POST", "/v1/operator/namespaces/remove", map[string]string{"namespace": name, "reason": reason})
	if err != nil {
		return err
	}
	var resp struct {
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clierr.Failure("could not parse the gateway's reply: %w", err)
	}
	fmt.Printf("Namespace removed: %s\n", resp.Namespace)
	return nil
}
