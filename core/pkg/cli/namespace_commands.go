package cli

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/constants"
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
	case "list":
		handleNamespaceList()
	case "repair":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: orama namespace repair <namespace_name>\n")
			os.Exit(1)
		}
		handleNamespaceRepair(args[1])
	case "enable":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: orama namespace enable <feature> --namespace <name>\n")
			fmt.Fprintf(os.Stderr, "Features: webrtc\n")
			os.Exit(1)
		}
		handleNamespaceEnable(args[1:])
	case "disable":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: orama namespace disable <feature> --namespace <name>\n")
			fmt.Fprintf(os.Stderr, "Features: webrtc\n")
			os.Exit(1)
		}
		handleNamespaceDisable(args[1:])
	case "webrtc-status":
		var ns string
		fs := flag.NewFlagSet("namespace webrtc-status", flag.ExitOnError)
		fs.StringVar(&ns, "namespace", "", "Namespace name")
		_ = fs.Parse(args[1:])
		if ns == "" {
			fmt.Fprintf(os.Stderr, "Usage: orama namespace webrtc-status --namespace <name>\n")
			os.Exit(1)
		}
		handleNamespaceWebRTCStatus(ns)
	case "keys":
		handleNamespaceKeys(args[1:])
	case "help":
		showNamespaceHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown namespace command: %s\n", subcommand)
		showNamespaceHelp()
		os.Exit(1)
	}
}

// handleNamespaceKeys drives scoped API-key management (bugboard #148):
//   orama namespace keys create --scope <profile|grants> [--label ...]
//   orama namespace keys list
//   orama namespace keys revoke --id <n>
//   orama namespace keys revoke-legacy
func handleNamespaceKeys(args []string) {
	if len(args) == 0 {
		showNamespaceKeysHelp()
		return
	}
	switch args[0] {
	case "create":
		handleNamespaceKeysCreate(args[1:])
	case "list", "ls":
		handleNamespaceKeysList(args[1:])
	case "revoke":
		handleNamespaceKeysRevoke(args[1:])
	case "revoke-legacy":
		handleNamespaceKeysRevokeLegacy(args[1:])
	default:
		showNamespaceKeysHelp()
		os.Exit(1)
	}
}

func showNamespaceKeysHelp() {
	fmt.Printf("Scoped API-key management (bugboard #148)\n\n")
	fmt.Printf("Usage: orama namespace keys <subcommand>\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  create --scope <profile|grants> [--label L] [--namespace NS]\n")
	fmt.Printf("      Mint a new key. Profiles: invoke-only | app-runtime | admin.\n")
	fmt.Printf("      Or an explicit grant list, e.g. --scope \"invoke,storage,push\".\n")
	fmt.Printf("  list [--namespace NS]                 List keys (id, scopes, status).\n")
	fmt.Printf("  revoke --id N [--namespace NS]        Revoke a single key by id.\n")
	fmt.Printf("  revoke-legacy [--force] [--namespace NS]\n")
	fmt.Printf("      Revoke ALL legacy (unscoped) keys — the cutover step.\n\n")
	fmt.Printf("Note: key management requires an admin-scoped key (or the owner wallet).\n")
}

// keysDo performs an authenticated JSON request to the gateway and returns the
// decoded body + status. Exits on transport failure.
func keysDo(method, url, apiKey string, body io.Reader) (map[string]interface{}, int) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to gateway: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return result, resp.StatusCode
}

func keysErr(result map[string]interface{}) string {
	if e, ok := result["error"].(string); ok && e != "" {
		return e
	}
	return "unknown error"
}

func handleNamespaceKeysCreate(args []string) {
	var ns, scope, label string
	fs := flag.NewFlagSet("namespace keys create", flag.ExitOnError)
	fs.StringVar(&ns, "namespace", "", "Namespace name")
	fs.StringVar(&scope, "scope", "", "Profile (invoke-only|app-runtime|admin) or grant list")
	fs.StringVar(&label, "label", "", "Human label for the key")
	_ = fs.Parse(args)
	if strings.TrimSpace(scope) == "" {
		fmt.Fprintf(os.Stderr, "Usage: orama namespace keys create --scope <profile|grants> [--label L] [--namespace NS]\n")
		os.Exit(1)
	}
	gatewayURL, apiKey := loadAuthForNamespace(ns)
	payload, _ := json.Marshal(map[string]string{"scope": scope, "label": label})
	result, status := keysDo(http.MethodPost, gatewayURL+"/v1/namespace/keys", apiKey, bytes.NewReader(payload))
	if status != http.StatusCreated && status != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Failed to create key: %s\n", keysErr(result))
		os.Exit(1)
	}
	fmt.Printf("API key created.\n")
	fmt.Printf("  id:        %v\n", result["id"])
	fmt.Printf("  scopes:    %v\n", result["scopes"])
	fmt.Printf("  namespace: %v\n", result["namespace"])
	if l, _ := result["label"].(string); l != "" {
		fmt.Printf("  label:     %s\n", l)
	}
	fmt.Printf("\n  API KEY (shown once — store it now):\n  %v\n", result["api_key"])
}

func handleNamespaceKeysList(args []string) {
	var ns string
	fs := flag.NewFlagSet("namespace keys list", flag.ExitOnError)
	fs.StringVar(&ns, "namespace", "", "Namespace name")
	_ = fs.Parse(args)
	gatewayURL, apiKey := loadAuthForNamespace(ns)
	result, status := keysDo(http.MethodGet, gatewayURL+"/v1/namespace/keys", apiKey, nil)
	if status != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Failed to list keys: %s\n", keysErr(result))
		os.Exit(1)
	}
	keys, _ := result["keys"].([]interface{})
	if len(keys) == 0 {
		fmt.Println("No API keys found.")
		return
	}
	fmt.Printf("API keys for namespace '%v' (%d):\n\n", result["namespace"], len(keys))
	for _, k := range keys {
		km, _ := k.(map[string]interface{})
		scopes, _ := km["scopes"].(string)
		if scopes == "" {
			scopes = "(legacy: admin)"
		}
		state := "active"
		if rv, _ := km["revoked_at"].(string); rv != "" {
			state = "REVOKED"
		}
		label, _ := km["name"].(string)
		fmt.Printf("  #%-4v  %-8s  %-40s  %s\n", km["id"], state, scopes, label)
	}
}

func handleNamespaceKeysRevoke(args []string) {
	var ns string
	var id int
	fs := flag.NewFlagSet("namespace keys revoke", flag.ExitOnError)
	fs.StringVar(&ns, "namespace", "", "Namespace name")
	fs.IntVar(&id, "id", 0, "Key id to revoke")
	_ = fs.Parse(args)
	if id <= 0 {
		fmt.Fprintf(os.Stderr, "Usage: orama namespace keys revoke --id <n> [--namespace NS]\n")
		os.Exit(1)
	}
	gatewayURL, apiKey := loadAuthForNamespace(ns)
	result, status := keysDo(http.MethodDelete, fmt.Sprintf("%s/v1/namespace/keys/%d", gatewayURL, id), apiKey, nil)
	if status != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Failed to revoke key: %s\n", keysErr(result))
		os.Exit(1)
	}
	fmt.Printf("Key %d revoked.\n", id)
}

func handleNamespaceKeysRevokeLegacy(args []string) {
	var ns string
	var force bool
	fs := flag.NewFlagSet("namespace keys revoke-legacy", flag.ExitOnError)
	fs.StringVar(&ns, "namespace", "", "Namespace name")
	fs.BoolVar(&force, "force", false, "Skip confirmation prompt")
	_ = fs.Parse(args)
	gatewayURL, apiKey := loadAuthForNamespace(ns)
	if !force {
		fmt.Printf("This will revoke ALL legacy (unscoped) API keys for this namespace.\n")
		fmt.Printf("Any consumer still using an old omnipotent key will stop working.\n")
		fmt.Printf("Type 'revoke' to confirm: ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if strings.TrimSpace(scanner.Text()) != "revoke" {
			fmt.Println("Aborted.")
			os.Exit(1)
		}
	}
	result, status := keysDo(http.MethodPost, gatewayURL+"/v1/namespace/keys/revoke-legacy", apiKey, nil)
	if status != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Failed to revoke legacy keys: %s\n", keysErr(result))
		os.Exit(1)
	}
	fmt.Printf("Revoked %v legacy key(s).\n", result["revoked"])
}

func showNamespaceHelp() {
	fmt.Printf("Namespace Management Commands\n\n")
	fmt.Printf("Usage: orama namespace <subcommand>\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  list                          - List namespaces owned by the current wallet\n")
	fmt.Printf("  delete                        - Delete the current namespace and all its resources\n")
	fmt.Printf("  repair <namespace>            - Repair an under-provisioned namespace cluster\n")
	fmt.Printf("  enable webrtc --namespace NS  - Enable WebRTC (SFU + TURN) for a namespace\n")
	fmt.Printf("  disable webrtc --namespace NS - Disable WebRTC for a namespace\n")
	fmt.Printf("  enable webrtc-stealth --namespace NS  - Enable stealth TURNS over :443 (feat-124)\n")
	fmt.Printf("  disable webrtc-stealth --namespace NS - Disable stealth TURNS\n")
	fmt.Printf("  webrtc-status --namespace NS  - Show WebRTC service status\n")
	fmt.Printf("  help                          - Show this help message\n\n")
	fmt.Printf("Flags:\n")
	fmt.Printf("  --force       - Skip confirmation prompt (delete only)\n")
	fmt.Printf("  --namespace   - Namespace name (enable/disable/webrtc-status)\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  orama namespace list\n")
	fmt.Printf("  orama namespace delete\n")
	fmt.Printf("  orama namespace delete --force\n")
	fmt.Printf("  orama namespace repair anchat\n")
	fmt.Printf("  orama namespace enable webrtc --namespace myapp\n")
	fmt.Printf("  orama namespace disable webrtc --namespace myapp\n")
	fmt.Printf("  orama namespace webrtc-status --namespace myapp\n")
}

func handleNamespaceRepair(namespaceName string) {
	fmt.Printf("Repairing namespace cluster '%s'...\n", namespaceName)

	// Call the internal repair endpoint on the local gateway
	url := fmt.Sprintf("http://localhost:%d/v1/internal/namespace/repair?namespace=%s", constants.GatewayAPIPort, namespaceName)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("X-Orama-Internal-Auth", "namespace-coordination")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to local gateway (is the node running?): %v\n", err)
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
		fmt.Fprintf(os.Stderr, "Repair failed: %s\n", errMsg)
		os.Exit(1)
	}

	fmt.Printf("Namespace '%s' cluster repaired successfully.\n", namespaceName)
	if msg, ok := result["message"].(string); ok {
		fmt.Printf("  %s\n", msg)
	}
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
		fmt.Printf("  - All deployments and their processes\n")
		fmt.Printf("  - RQLite cluster (3 nodes)\n")
		fmt.Printf("  - Olric cache cluster (3 nodes)\n")
		fmt.Printf("  - Gateway instances\n")
		fmt.Printf("  - API keys and credentials\n")
		fmt.Printf("  - IPFS content and DNS records\n\n")
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

	// Clean up local credentials for the deleted namespace
	if store.RemoveCredentialByNamespace(gatewayURL, namespace) {
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clean up local credentials: %v\n", err)
		} else {
			fmt.Printf("Local credentials for '%s' cleared.\n", namespace)
		}
	}

	fmt.Printf("Run 'orama auth login' to create a new namespace.\n")
}

func handleNamespaceEnable(args []string) {
	feature := args[0]
	if feature == "webrtc-stealth" {
		handleNamespaceStealthToggle(args[1:], true)
		return
	}
	if feature != "webrtc" {
		fmt.Fprintf(os.Stderr, "Unknown feature: %s\nSupported features: webrtc, webrtc-stealth\n", feature)
		os.Exit(1)
	}

	var ns string
	fs := flag.NewFlagSet("namespace enable webrtc", flag.ExitOnError)
	fs.StringVar(&ns, "namespace", "", "Namespace name")
	_ = fs.Parse(args[1:])

	if ns == "" {
		fmt.Fprintf(os.Stderr, "Usage: orama namespace enable webrtc --namespace <name>\n")
		os.Exit(1)
	}

	gatewayURL, apiKey := loadAuthForNamespace(ns)

	fmt.Printf("Enabling WebRTC for namespace '%s'...\n", ns)
	fmt.Printf("This will provision SFU (3 nodes) and TURN (2 nodes) services.\n")

	url := fmt.Sprintf("%s/v1/namespace/webrtc/enable", gatewayURL)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
		fmt.Fprintf(os.Stderr, "Failed to enable WebRTC: %s\n", errMsg)
		os.Exit(1)
	}

	fmt.Printf("WebRTC enabled for namespace '%s'.\n", ns)
	fmt.Printf("  SFU instances: 3 nodes (signaling via WireGuard)\n")
	fmt.Printf("  TURN instances: 2 nodes (relay on public IPs)\n")
}

// handleNamespaceStealthToggle drives /v1/namespace/webrtc/stealth/{enable|disable}
// (feat-124 — censorship-resistant TURNS over :443).
func handleNamespaceStealthToggle(args []string, enable bool) {
	verb := "disable"
	if enable {
		verb = "enable"
	}

	var ns string
	fs := flag.NewFlagSet("namespace "+verb+" webrtc-stealth", flag.ExitOnError)
	fs.StringVar(&ns, "namespace", "", "Namespace name")
	_ = fs.Parse(args)

	if ns == "" {
		fmt.Fprintf(os.Stderr, "Usage: orama namespace %s webrtc-stealth --namespace <name>\n", verb)
		os.Exit(1)
	}

	gatewayURL, apiKey := loadAuthForNamespace(ns)

	if enable {
		fmt.Printf("Enabling WebRTC stealth (TURNS over :443) for namespace '%s'...\n", ns)
		fmt.Printf("This provisions a Let's Encrypt cert for the neutral stealth host and may take up to ~2 minutes.\n")
	} else {
		fmt.Printf("Disabling WebRTC stealth for namespace '%s'...\n", ns)
	}

	url := fmt.Sprintf("%s/v1/namespace/webrtc/stealth/%s", gatewayURL, verb)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
		fmt.Fprintf(os.Stderr, "Failed to %s WebRTC stealth: %s\n", verb, errMsg)
		os.Exit(1)
	}

	if enable {
		fmt.Printf("WebRTC stealth enabled for namespace '%s'.\n", ns)
		fmt.Printf("  turn.credentials now advertises the full URI ladder including turns:<stealth-host>:443.\n")
		fmt.Printf("  Make sure the SNI router is enabled on the TURN nodes (node.yaml sni_router.enabled).\n")
	} else {
		fmt.Printf("WebRTC stealth disabled for namespace '%s'.\n", ns)
	}
}

func handleNamespaceDisable(args []string) {
	feature := args[0]
	if feature == "webrtc-stealth" {
		handleNamespaceStealthToggle(args[1:], false)
		return
	}
	if feature != "webrtc" {
		fmt.Fprintf(os.Stderr, "Unknown feature: %s\nSupported features: webrtc, webrtc-stealth\n", feature)
		os.Exit(1)
	}

	var ns string
	fs := flag.NewFlagSet("namespace disable webrtc", flag.ExitOnError)
	fs.StringVar(&ns, "namespace", "", "Namespace name")
	_ = fs.Parse(args[1:])

	if ns == "" {
		fmt.Fprintf(os.Stderr, "Usage: orama namespace disable webrtc --namespace <name>\n")
		os.Exit(1)
	}

	gatewayURL, apiKey := loadAuthForNamespace(ns)

	fmt.Printf("Disabling WebRTC for namespace '%s'...\n", ns)

	url := fmt.Sprintf("%s/v1/namespace/webrtc/disable", gatewayURL)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
		fmt.Fprintf(os.Stderr, "Failed to disable WebRTC: %s\n", errMsg)
		os.Exit(1)
	}

	fmt.Printf("WebRTC disabled for namespace '%s'.\n", ns)
	fmt.Printf("  SFU and TURN services stopped, ports deallocated, DNS records removed.\n")
}

func handleNamespaceWebRTCStatus(ns string) {
	gatewayURL, apiKey := loadAuthForNamespace(ns)

	url := fmt.Sprintf("%s/v1/namespace/webrtc/status", gatewayURL)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
		fmt.Fprintf(os.Stderr, "Failed to get WebRTC status: %s\n", errMsg)
		os.Exit(1)
	}

	enabled, _ := result["enabled"].(bool)
	if !enabled {
		fmt.Printf("WebRTC is not enabled for namespace '%s'.\n", ns)
		fmt.Printf("  Enable with: orama namespace enable webrtc --namespace %s\n", ns)
		return
	}

	fmt.Printf("WebRTC Status for namespace '%s'\n\n", ns)
	fmt.Printf("  Enabled:          yes\n")
	if sfuCount, ok := result["sfu_node_count"].(float64); ok {
		fmt.Printf("  SFU nodes:        %.0f\n", sfuCount)
	}
	if turnCount, ok := result["turn_node_count"].(float64); ok {
		fmt.Printf("  TURN nodes:       %.0f\n", turnCount)
	}
	if ttl, ok := result["turn_credential_ttl"].(float64); ok {
		fmt.Printf("  TURN cred TTL:    %.0fs\n", ttl)
	}
	if enabledBy, ok := result["enabled_by"].(string); ok {
		fmt.Printf("  Enabled by:       %s\n", enabledBy)
	}
	if enabledAt, ok := result["enabled_at"].(string); ok {
		fmt.Printf("  Enabled at:       %s\n", enabledAt)
	}
}

// loadAuthForNamespace loads credentials and returns the gateway URL and API key.
// Exits with an error message if not authenticated.
func loadAuthForNamespace(ns string) (gatewayURL, apiKey string) {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	gatewayURL = getGatewayURL()
	creds := store.GetDefaultCredential(gatewayURL)

	if creds == nil || !creds.IsValid() {
		fmt.Fprintf(os.Stderr, "Not authenticated. Run 'orama auth login' first.\n")
		os.Exit(1)
	}

	return gatewayURL, creds.APIKey
}

func handleNamespaceList() {
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

	// Make GET request to namespace list endpoint
	url := fmt.Sprintf("%s/v1/namespace/list", gatewayURL)
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
		fmt.Fprintf(os.Stderr, "Failed to list namespaces: %s\n", errMsg)
		os.Exit(1)
	}

	namespaces, _ := result["namespaces"].([]interface{})
	if len(namespaces) == 0 {
		fmt.Println("No namespaces found.")
		return
	}

	activeNS := creds.Namespace

	fmt.Printf("Namespaces (%d):\n\n", len(namespaces))
	for _, ns := range namespaces {
		nsMap, _ := ns.(map[string]interface{})
		name, _ := nsMap["name"].(string)
		status, _ := nsMap["cluster_status"].(string)

		marker := "  "
		if name == activeNS {
			marker = "* "
		}

		fmt.Printf("%s%-20s  cluster: %s\n", marker, name, status)
	}
	fmt.Printf("\n* = active namespace\n")
}
