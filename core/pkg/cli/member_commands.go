package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
)

// Who else may work in a namespace.
//
// There was one setting: the owner, and everybody else refused. A second person
// on a team was handed the owner's credentials or nothing at all, which is not a
// permission system so much as the absence of one. These commands are the roles
// made usable from a terminal.

// MembersList prints who holds a grant in a namespace.
func MembersList(ns string) error {
	gatewayURL, apiKey, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	result, err := nsRequest("list the members", http.MethodGet,
		gatewayURL+"/v1/namespace/members", apiKey, nil)
	if err != nil {
		return err
	}

	members, _ := result["members"].([]any)
	if len(members) == 0 {
		fmt.Println("No members found.")
		return nil
	}

	fmt.Printf("Members of namespace '%v' (%d):\n\n", result["namespace"], len(members))
	for _, m := range members {
		entry, _ := m.(map[string]any)
		identifier, _ := entry["identifier"].(string)
		kind, _ := entry["type"].(string)
		if kind == "service_account" {
			// The identifier is the key's hash, which is not something anyone
			// can act on. Say what it is rather than printing 64 hex bytes.
			identifier = "(api key)"
		}
		fmt.Printf("  %-9v  %-44s", entry["role"], identifier)
		if name, _ := entry["display_name"].(string); name != "" {
			fmt.Printf("  %s", name)
		}
		if expires, _ := entry["expires_at"].(string); expires != "" {
			fmt.Printf("  expires %s", expires)
		}
		if resource, _ := entry["resource"].(string); resource != "" {
			fmt.Printf("  [%s — NOT ENFORCED]", resource)
		}
		fmt.Println()
	}
	return nil
}

// MembersAdd gives a wallet a role in a namespace.
func MembersAdd(ns, wallet, role, resource, displayName string, expiresInHours int) error {
	if strings.TrimSpace(wallet) == "" {
		return clierr.Usage("which wallet: orama members add <wallet> --role <role>")
	}
	if strings.TrimSpace(role) == "" {
		return clierr.Usage("--role is required: admin (the control plane), runtime (the data plane), " +
			"or reader (a member with no grant). Ownership is not granted; transfer it instead")
	}

	gatewayURL, apiKey, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	body := map[string]any{"wallet": wallet, "role": role}
	if resource != "" {
		body["resource"] = resource
	}
	if displayName != "" {
		body["display_name"] = displayName
	}
	if expiresInHours > 0 {
		body["expires_in_hours"] = expiresInHours
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return clierr.Failure("failed to encode the request: %w", err)
	}

	result, err := nsRequest("add the member", http.MethodPost,
		gatewayURL+"/v1/namespace/members", apiKey, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	fmt.Printf("%v is now %v in namespace '%v'.\n", result["wallet"], result["role"], result["namespace"])
	if warning, _ := result["warning"].(string); warning != "" {
		fmt.Printf("\n  WARNING: %s\n", warning)
	}
	return nil
}

// MembersRemove takes a wallet's grant away.
func MembersRemove(ns, wallet string) error {
	if strings.TrimSpace(wallet) == "" {
		return clierr.Usage("which wallet: orama members remove <wallet>")
	}

	gatewayURL, apiKey, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	result, err := nsRequest("remove the member", http.MethodDelete,
		gatewayURL+"/v1/namespace/members/"+strings.TrimSpace(wallet), apiKey, nil)
	if err != nil {
		return err
	}

	fmt.Printf("%v no longer holds a grant in namespace '%v'.\n", result["wallet"], result["namespace"])
	return nil
}

// MembersTransfer hands a namespace to another wallet.
func MembersTransfer(ns, wallet string, force bool) error {
	if strings.TrimSpace(wallet) == "" {
		return clierr.Usage("which wallet: orama members transfer <wallet>")
	}

	gatewayURL, apiKey, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	if !force {
		fmt.Printf("This hands namespace '%s' to %s.\n", ns, wallet)
		fmt.Printf("You keep an admin grant, but they become the owner: only they can transfer it back.\n")
		if err := confirmExact("Type 'transfer' to confirm: ", "transfer"); err != nil {
			return err
		}
	}

	payload, err := json.Marshal(map[string]string{"wallet": wallet})
	if err != nil {
		return clierr.Failure("failed to encode the request: %w", err)
	}

	result, err := nsRequest("transfer the namespace", http.MethodPost,
		gatewayURL+"/v1/namespace/members/transfer", apiKey, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	fmt.Printf("Namespace '%v' now belongs to %v. You are an admin of it.\n",
		result["namespace"], result["owner"])
	return nil
}
