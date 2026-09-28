package clustercmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"github.com/spf13/cobra"
)

// Cmd is who may create a namespace on this cluster, and how many one wallet
// may own. Both are operator acts: the operator grant and a wallet on the
// operator list.
var Cmd = &cobra.Command{
	Use:   "cluster",
	Short: "Choose who may create namespaces on this cluster",
	Long: `Who may create a namespace on this cluster, and how many one wallet may own.

A new cluster allows only its operators. A cluster that already had a
namespace besides the seeded default, a node, or an operator when this was
upgraded stays open — any signed-in wallet — until an operator changes it.
The per-wallet cap stays 10 until an operator raises or lowers it.

Changing a setting or the creator list needs the operator grant and a wallet
on the operator list, and is written to the audit trail.`,
}

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Show or change namespace-creation settings",
}

var settingsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show who may create namespaces, and the per-wallet cap",
	Args:  cobra.NoArgs,
	RunE:  showSettings,
}

var settingsSetCmd = &cobra.Command{
	Use:   "set <setting> <value>",
	Short: "Change namespace creation or the per-wallet cap",
	Args:  cobra.ExactArgs(2),
	RunE:  setSetting,
}

var creatorsCmd = &cobra.Command{
	Use:   "creators",
	Short: "Wallets that may create namespaces when creation is allowlist",
	Long: `The allowlist consulted when namespace creation is allowlist.

An operator is not on it unless added. An empty list lets nobody create a
namespace, and removing the last wallet does not lock operators out.`,
}

var creatorsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List wallets allowed to create namespaces",
	Args:  cobra.NoArgs,
	RunE:  listCreators,
}

var creatorsAddCmd = &cobra.Command{
	Use:   "add <wallet>",
	Short: "Let a wallet create namespaces when creation is allowlist",
	Args:  cobra.ExactArgs(1),
	RunE:  addCreator,
}

var creatorsRemoveCmd = &cobra.Command{
	Use:   "remove <wallet>",
	Short: "Take a wallet off the namespace-creator list",
	Args:  cobra.ExactArgs(1),
	RunE:  removeCreator,
}

func init() {
	settingsSetCmd.Long = fmt.Sprintf(`namespace-creation is operators, allowlist or open.

  operators   only wallets on the operator list
  allowlist   only wallets added with orama cluster creators add
  open        any signed-in wallet

max-namespaces-per-wallet is an integer from 1 to %d. The default is %d.`,
		operator.MaxNamespacesPerWalletCeiling, operator.DefaultMaxNamespacesPerWallet)
	settingsCmd.AddCommand(settingsShowCmd)
	settingsCmd.AddCommand(settingsSetCmd)
	creatorsCmd.AddCommand(creatorsListCmd)
	creatorsCmd.AddCommand(creatorsAddCmd)
	creatorsCmd.AddCommand(creatorsRemoveCmd)
	Cmd.AddCommand(settingsCmd)
	Cmd.AddCommand(creatorsCmd)
}

func showSettings(cmd *cobra.Command, args []string) error {
	raw, err := shared.Request("GET", "/v1/operator/settings", nil)
	if err != nil {
		return err
	}
	var resp struct {
		Mode string `json:"namespace_creation"`
		Cap  int    `json:"max_namespaces_per_wallet"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clierr.Failure("could not parse the gateway's reply: %w", err)
	}
	if resp.Mode == "" {
		return clierr.Failure("the gateway reported no namespace-creation setting")
	}
	fmt.Printf("namespace-creation: %s\n", resp.Mode)
	fmt.Printf("max-namespaces-per-wallet: %d\n", resp.Cap)
	return nil
}

func setSetting(cmd *cobra.Command, args []string) error {
	path, body, err := parseClusterSetting(args[0], args[1])
	if err != nil {
		return err
	}
	raw, err := shared.Request("PUT", path, body)
	if err != nil {
		return err
	}
	var resp struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clierr.Failure("could not parse the gateway's reply: %w", err)
	}
	switch resp.Key {
	case operator.SettingNamespaceCreation:
		fmt.Printf("Namespace creation is %s.\n", resp.Value)
	case operator.SettingMaxNamespacesPerWallet:
		fmt.Printf("A wallet may own %s namespaces.\n", resp.Value)
	default:
		fmt.Printf("%s is %s.\n", resp.Key, resp.Value)
	}
	return nil
}

// parseClusterSetting checks the two settings the gateway accepts before the
// request is sent, so a typo is a usage error rather than a round trip.
func parseClusterSetting(name, value string) (string, any, error) {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	switch name {
	case "namespace-creation":
		switch value {
		case operator.CreationOperators, operator.CreationAllowlist, operator.CreationOpen:
			return "/v1/operator/settings/namespace-creation", map[string]string{"value": value}, nil
		default:
			return "", nil, clierr.Usage("namespace-creation is operators, allowlist or open")
		}
	case "max-namespaces-per-wallet":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > operator.MaxNamespacesPerWalletCeiling {
			return "", nil, clierr.Usage("max-namespaces-per-wallet is an integer from 1 to %d", operator.MaxNamespacesPerWalletCeiling)
		}
		return "/v1/operator/settings/max-namespaces-per-wallet", map[string]any{"value": n}, nil
	default:
		return "", nil, clierr.Usage("unknown setting %q; expected namespace-creation or max-namespaces-per-wallet", name)
	}
}

func listCreators(cmd *cobra.Command, args []string) error {
	raw, err := shared.Request("GET", "/v1/operator/creators", nil)
	if err != nil {
		return err
	}
	var resp struct {
		Creators []struct {
			Wallet  string `json:"wallet"`
			AddedBy string `json:"added_by"`
		} `json:"creators"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clierr.Failure("could not parse the gateway's reply: %w", err)
	}
	if len(resp.Creators) == 0 {
		fmt.Println("No namespace creators.")
		return nil
	}
	for _, c := range resp.Creators {
		fmt.Printf("%s  added by %s\n", c.Wallet, c.AddedBy)
	}
	return nil
}

func addCreator(cmd *cobra.Command, args []string) error {
	wallet := strings.TrimSpace(args[0])
	if wallet == "" {
		return clierr.Usage("wallet is required")
	}
	raw, err := shared.Request("POST", "/v1/operator/creators", map[string]string{"wallet": wallet})
	if err != nil {
		return err
	}
	var resp struct {
		Wallet string `json:"wallet"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clierr.Failure("could not parse the gateway's reply: %w", err)
	}
	fmt.Printf("Creator added: %s\n", resp.Wallet)
	return nil
}

func removeCreator(cmd *cobra.Command, args []string) error {
	wallet := strings.TrimSpace(args[0])
	if wallet == "" {
		return clierr.Usage("wallet is required")
	}
	raw, err := shared.Request("DELETE", "/v1/operator/creators/"+url.PathEscape(wallet), nil)
	if err != nil {
		return err
	}
	var resp struct {
		Wallet string `json:"wallet"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clierr.Failure("could not parse the gateway's reply: %w", err)
	}
	fmt.Printf("Creator removed: %s\n", resp.Wallet)
	return nil
}
