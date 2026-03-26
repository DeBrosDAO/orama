package functions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	secretsDeleteForce bool
	secretsFromFile    string
)

// SecretsCmd is the parent command for secrets management.
var SecretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage function secrets",
	Long: `Set, list, and delete encrypted secrets for your serverless functions.

Functions access secrets at runtime via the get_secret() host function.
Secrets are scoped to your namespace and encrypted at rest with AES-256-GCM.

Examples:
  orama function secrets set API_KEY "sk-abc123"
  orama function secrets set CERT_PEM --from-file ./cert.pem
  orama function secrets list
  orama function secrets delete API_KEY`,
}

// SecretsSetCmd stores an encrypted secret.
var SecretsSetCmd = &cobra.Command{
	Use:   "set <name> [value]",
	Short: "Set a secret",
	Long:  `Stores an encrypted secret. Functions access it via get_secret("name"). If --from-file is used, value is read from the file instead.`,
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runSecretsSet,
}

// SecretsListCmd lists secret names.
var SecretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secret names",
	Long:  "Lists all secret names in the current namespace. Values are never shown.",
	Args:  cobra.NoArgs,
	RunE:  runSecretsList,
}

// SecretsDeleteCmd deletes a secret.
var SecretsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a secret",
	Long:  "Permanently deletes a secret. Functions will no longer be able to access it.",
	Args:  cobra.ExactArgs(1),
	RunE:  runSecretsDelete,
}

func init() {
	SecretsCmd.AddCommand(SecretsSetCmd)
	SecretsCmd.AddCommand(SecretsListCmd)
	SecretsCmd.AddCommand(SecretsDeleteCmd)

	SecretsSetCmd.Flags().StringVar(&secretsFromFile, "from-file", "", "Read secret value from a file")
	SecretsDeleteCmd.Flags().BoolVarP(&secretsDeleteForce, "force", "f", false, "Skip confirmation prompt")
}

func runSecretsSet(cmd *cobra.Command, args []string) error {
	name := args[0]

	var value string
	if secretsFromFile != "" {
		data, err := os.ReadFile(secretsFromFile)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", secretsFromFile, err)
		}
		value = string(data)
	} else if len(args) >= 2 {
		value = args[1]
	} else {
		return fmt.Errorf("secret value required: provide as argument or use --from-file")
	}

	body, _ := json.Marshal(map[string]string{
		"name":  name,
		"value": value,
	})

	resp, err := apiRequest("PUT", "/v1/functions/secrets", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	fmt.Printf("Secret %q set successfully.\n", name)
	return nil
}

func runSecretsList(cmd *cobra.Command, args []string) error {
	result, err := apiGet("/v1/functions/secrets")
	if err != nil {
		return err
	}

	secrets, _ := result["secrets"].([]interface{})
	if len(secrets) == 0 {
		fmt.Println("No secrets found.")
		return nil
	}

	fmt.Printf("Secrets (%d):\n", len(secrets))
	for _, s := range secrets {
		fmt.Printf("  %s\n", s)
	}
	return nil
}

func runSecretsDelete(cmd *cobra.Command, args []string) error {
	name := args[0]

	if !secretsDeleteForce {
		fmt.Printf("Are you sure you want to delete secret %q? [y/N] ", name)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	result, err := apiDelete("/v1/functions/secrets/" + name)
	if err != nil {
		return err
	}

	if msg, ok := result["message"]; ok {
		fmt.Println(msg)
	} else {
		fmt.Printf("Secret %q deleted.\n", name)
	}
	return nil
}
