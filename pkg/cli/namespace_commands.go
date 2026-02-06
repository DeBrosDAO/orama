package cli

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// HandleNamespaceCommand handles namespace management commands
func HandleNamespaceCommand(args []string) {
	if len(args) == 0 {
		showNamespaceHelp()
		return
	}

	subcommand := args[0]
	switch subcommand {
	case "delete":
		var force bool
		fs := flag.NewFlagSet("namespace delete", flag.ExitOnError)
		fs.BoolVar(&force, "force", false, "Skip confirmation prompt")
		_ = fs.Parse(args[1:])
		handleNamespaceDelete(force)
	case "help":
		showNamespaceHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown namespace command: %s\n", subcommand)
		showNamespaceHelp()
		os.Exit(1)
	}
}

func showNamespaceHelp() {
	fmt.Printf("Namespace Management Commands\n\n")
	fmt.Printf("Usage: orama namespace <subcommand>\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  delete      - Delete the current namespace and all its resources\n")
	fmt.Printf("  help        - Show this help message\n\n")
	fmt.Printf("Flags:\n")
	fmt.Printf("  --force     - Skip confirmation prompt\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  orama namespace delete\n")
	fmt.Printf("  orama namespace delete --force\n")
}

func handleNamespaceDelete(force bool) {
	// Load credentials
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	gatewayURL := getGatewayURL()
	creds := store.GetDefaultCredential(gatewayURL)

	if creds == nil || !creds.IsValid() {
		fmt.Fprintf(os.Stderr, "Not authenticated. Run 'orama auth login' first.\n")
		os.Exit(1)
	}

	namespace := creds.Namespace
	if namespace == "" || namespace == "default" {
		fmt.Fprintf(os.Stderr, "Cannot delete default namespace.\n")
		os.Exit(1)
	}

	// Confirm deletion
	if !force {
		fmt.Printf("This will permanently delete namespace '%s' and all its resources:\n", namespace)
		fmt.Printf("  - RQLite cluster (3 nodes)\n")
		fmt.Printf("  - Olric cache cluster (3 nodes)\n")
		fmt.Printf("  - Gateway instances\n")
		fmt.Printf("  - API keys and credentials\n\n")
		fmt.Printf("Type the namespace name to confirm: ")

		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		input := strings.TrimSpace(scanner.Text())

		if input != namespace {
			fmt.Println("Aborted - namespace name did not match.")
			os.Exit(1)
		}
	}

	fmt.Printf("Deleting namespace '%s'...\n", namespace)

	// Make DELETE request to gateway
	url := fmt.Sprintf("%s/v1/namespace/delete", gatewayURL)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+creds.APIKey)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to gateway: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != http.StatusOK {
		errMsg := "unknown error"
		if e, ok := result["error"].(string); ok {
			errMsg = e
		}
		fmt.Fprintf(os.Stderr, "Failed to delete namespace: %s\n", errMsg)
		os.Exit(1)
	}

	fmt.Printf("Namespace '%s' deleted successfully.\n", namespace)
	fmt.Printf("Run 'orama auth login' to create a new namespace.\n")
}
