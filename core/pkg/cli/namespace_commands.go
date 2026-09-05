package cli

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// nsRequestTimeout bounds one namespace API call.
//
// Enabling WebRTC stealth waits on a Let's Encrypt issuance, which the command
// tells the operator may take about two minutes, so the bound has to clear it.
const nsRequestTimeout = 3 * time.Minute

// nsClient is the shared client for namespace API calls. Seven copies of this
// construction existed in this file, all identical.
var nsClient = &http.Client{
	Timeout:   nsRequestTimeout,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
}

// nsRequest performs one authenticated namespace API call and decodes the reply.
//
// what names the operation for the error message, as in "enable WebRTC".
// A non-2xx reply becomes an error carrying an exit code, so a script can tell
// "you are not authenticated" from "the gateway is unreachable" from "that
// namespace does not exist".
func nsRequest(what, method, url, token string, body io.Reader) (map[string]any, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, clierr.Failure("failed to build the %s request: %w", what, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := nsClient.Do(req)
	if err != nil {
		return nil, clierr.Unavailable("failed to reach the gateway to %s: %w", what, err)
	}
	defer resp.Body.Close()

	var result map[string]any
	// A body that is not JSON leaves result nil, and nsError falls back to the
	// status line. Decoding is not the operation being reported on.
	_ = json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return result, nsError(what, resp.StatusCode, result)
	}
	return result, nil
}

// nsError classifies a gateway refusal by status, and says what to do about it
// by code.
//
// The two answer different questions and both are needed: the status decides
// the exit code a script branches on, and the code decides the sentence a
// person reads. Reporting only the gateway's own message left "insufficient
// scope" as the whole of the advice.
func nsError(what string, status int, result map[string]any) error {
	message := "unknown error"
	if encoded, err := json.Marshal(result); err == nil {
		if refusal := auth.GatewayErrorFrom(status, encoded); refusal.Code != "" || refusal.Message != "" {
			message = refusal.Error()
		}
	}

	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return clierr.Auth("failed to %s: %s", what, message)
	case http.StatusNotFound:
		return clierr.NotFound("failed to %s: %s", what, message)
	case http.StatusConflict, http.StatusPreconditionFailed:
		return clierr.Conflict("failed to %s: %s", what, message)
	case http.StatusBadRequest:
		return clierr.Usage("failed to %s: %s", what, message)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return clierr.Unavailable("failed to %s: %s", what, message)
	default:
		return clierr.Failure("failed to %s: %s (HTTP %d)", what, message, status)
	}
}

// confirmExact reads a line and requires it to equal want.
//
// Typing the thing back is the confirmation, not y/n: a y/n is answered
// reflexively, and what these commands guard against is the right command
// aimed at the wrong namespace.
func confirmExact(prompt, want string) error {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	if strings.TrimSpace(scanner.Text()) != want {
		return clierr.Aborted("cancelled: what you typed did not match %q", want)
	}
	return nil
}

// --- API keys (bugboard #148) ---------------------------------------------

// NamespaceKeysCreate mints a scoped API key.
func NamespaceKeysCreate(ns, scope, label string, expiresInDays int) error {
	if strings.TrimSpace(scope) == "" {
		return clierr.Usage("--scope is required: a profile (invoke-only, app-runtime, admin) " +
			"or an explicit grant list such as \"invoke,storage,push\"")
	}

	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	body := map[string]any{"scope": scope, "label": label}
	if expiresInDays > 0 {
		body["expires_in_days"] = expiresInDays
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return clierr.Failure("failed to encode the request: %w", err)
	}

	result, err := nsRequest("create the key", http.MethodPost,
		gatewayURL+"/v1/namespace/keys", token, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	fmt.Printf("API key created.\n")
	fmt.Printf("  id:        %v\n", result["id"])
	fmt.Printf("  scopes:    %v\n", result["scopes"])
	fmt.Printf("  namespace: %v\n", result["namespace"])
	fmt.Printf("  expires:   %v\n", result["expires_at"])
	if l, _ := result["label"].(string); l != "" {
		fmt.Printf("  label:     %s\n", l)
	}
	fmt.Printf("\n  API KEY (shown once — store it now):\n  %v\n", result["api_key"])
	return nil
}

// NamespaceKeysList lists a namespace's API keys.
func NamespaceKeysList(ns string) error {
	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	result, err := nsRequest("list the keys", http.MethodGet,
		gatewayURL+"/v1/namespace/keys", token, nil)
	if err != nil {
		return err
	}

	keys, _ := result["keys"].([]any)
	if len(keys) == 0 {
		fmt.Println("No API keys found.")
		return nil
	}

	fmt.Printf("API keys for namespace '%v' (%d):\n\n", result["namespace"], len(keys))
	for _, k := range keys {
		km, _ := k.(map[string]any)
		scopes, _ := km["scopes"].(string)
		if scopes == "" {
			scopes = "(no grants: denies)"
		}
		state := "active"
		if rv, _ := km["revoked_at"].(string); rv != "" {
			state = "REVOKED"
		}
		label, _ := km["name"].(string)
		expires, _ := km["expires_at"].(string)
		fmt.Printf("  #%-4v  %-8s  %-40s  expires %-20s  %s", km["id"], state, scopes, expires, label)
		// A key that replaces another is a rotation in progress, and both are
		// live until the original's shortened expiry.
		if from, ok := km["rotated_from"]; ok && from != nil {
			fmt.Printf("  (rotated from #%v)", from)
		}
		fmt.Println()
	}
	return nil
}

// NamespaceKeysRotate mints a successor to a key and shortens the original's
// life to the overlap.
func NamespaceKeysRotate(ns string, id, overlapDays, expiresInDays int) error {
	if id <= 0 {
		return clierr.Usage("--id must be a positive key id; 'orama namespace keys list' shows them")
	}

	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	body := map[string]any{}
	if overlapDays > 0 {
		body["overlap_days"] = overlapDays
	}
	if expiresInDays > 0 {
		body["expires_in_days"] = expiresInDays
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return clierr.Failure("failed to encode the request: %w", err)
	}

	result, err := nsRequest("rotate the key", http.MethodPost,
		fmt.Sprintf("%s/v1/namespace/keys/%d/rotate", gatewayURL, id), token, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	fmt.Printf("Key %d rotated.\n", id)
	fmt.Printf("  new id:    %v\n", result["id"])
	fmt.Printf("  expires:   %v\n", result["expires_at"])
	fmt.Printf("  key #%d works until %v — deploy the new one before then.\n", id, result["previous_expires"])
	fmt.Printf("\n  API KEY (shown once — store it now):\n  %v\n", result["api_key"])
	return nil
}

// NamespaceKeysRevoke revokes one API key by id.
func NamespaceKeysRevoke(ns string, id int) error {
	if id <= 0 {
		return clierr.Usage("--id must be a positive key id; 'orama namespace keys list' shows them")
	}

	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	if _, err := nsRequest("revoke the key", http.MethodDelete,
		fmt.Sprintf("%s/v1/namespace/keys/%d", gatewayURL, id), token, nil); err != nil {
		return err
	}

	fmt.Printf("Key %d revoked.\n", id)
	return nil
}

// NamespaceKeysRevokeLegacy revokes every unscoped (legacy) key.
func NamespaceKeysRevokeLegacy(ns string, force bool) error {
	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	if !force {
		fmt.Printf("This will revoke ALL legacy (unscoped) API keys for this namespace.\n")
		fmt.Printf("Any consumer still using an old omnipotent key will stop working.\n")
		if err := confirmExact("Type 'revoke' to confirm: ", "revoke"); err != nil {
			return err
		}
	}

	result, err := nsRequest("revoke the legacy keys", http.MethodPost,
		gatewayURL+"/v1/namespace/keys/revoke-legacy", token, nil)
	if err != nil {
		return err
	}

	fmt.Printf("Revoked %v legacy key(s).\n", result["revoked"])
	return nil
}

// --- Namespace lifecycle ---------------------------------------------------

// NamespaceRepair repairs an under-provisioned namespace cluster.
//
// This one talks to the node's own gateway over loopback with the internal
// auth header, not to the operator's gateway, so it must be run on a node.
func NamespaceRepair(namespaceName string) error {
	fmt.Printf("Repairing namespace cluster '%s'...\n", namespaceName)

	url := fmt.Sprintf("http://localhost:%d/v1/internal/namespace/repair?namespace=%s",
		constants.GatewayAPIPort, namespaceName)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return clierr.Failure("failed to build the repair request: %w", err)
	}
	// This runs on a node, so the cluster secret is on disk here. The header
	// this replaces was a constant in the source: anything that could reach a
	// node's gateway port could repair — or mis-repair — any namespace on it.
	if err := signCoordinationRequest(req); err != nil {
		return clierr.Failure("%w", err)
	}

	resp, err := nsClient.Do(req)
	if err != nil {
		return clierr.Unavailable("failed to reach the local gateway (is the node running?): %w", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != http.StatusOK {
		return nsError("repair the namespace", resp.StatusCode, result)
	}

	fmt.Printf("Namespace '%s' cluster repaired.\n", namespaceName)
	if msg, ok := result["message"].(string); ok {
		fmt.Printf("  %s\n", msg)
	}
	return nil
}

// NamespaceDelete deletes the namespace the active credential belongs to.
func NamespaceDelete(force bool) error {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}

	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil || !creds.IsValid() {
		return clierr.Auth("not authenticated: run 'orama auth login'")
	}

	namespace := creds.Namespace
	if namespace == "" || namespace == "default" {
		return clierr.Usage("the default namespace cannot be deleted")
	}

	if !force {
		fmt.Printf("This will permanently delete namespace '%s' and all its resources:\n", namespace)
		fmt.Printf("  - All deployments and their processes\n")
		fmt.Printf("  - RQLite cluster (3 nodes)\n")
		fmt.Printf("  - Olric cache cluster (3 nodes)\n")
		fmt.Printf("  - Gateway instances\n")
		fmt.Printf("  - API keys and credentials\n")
		fmt.Printf("  - IPFS content and DNS records\n\n")
		if err := confirmExact("Type the namespace name to confirm: ", namespace); err != nil {
			return err
		}
	}

	fmt.Printf("Deleting namespace '%s'...\n", namespace)

	if _, err := nsRequest("delete the namespace", http.MethodDelete,
		gatewayURL+"/v1/namespace/delete", creds.APIKey, nil); err != nil {
		return err
	}

	fmt.Printf("Namespace '%s' deleted.\n", namespace)

	// The remote namespace is gone either way, so a failure to tidy the local
	// credential file is reported without failing the command.
	if store.RemoveCredentialByNamespace(gatewayURL, namespace) {
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clean up local credentials: %v\n", err)
		} else {
			fmt.Printf("Local credentials for '%s' cleared.\n", namespace)
		}
	}

	fmt.Printf("Run 'orama auth login' to create a new namespace.\n")
	return nil
}

// NamespaceList lists namespaces owned by the current wallet.
func NamespaceList(out *printer.Printer) error {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}

	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil || !creds.IsValid() {
		return clierr.Auth("not authenticated: run 'orama auth login'")
	}

	result, err := nsRequest("list namespaces", http.MethodGet,
		gatewayURL+"/v1/namespace/list", creds.APIKey, nil)
	if err != nil {
		return err
	}

	namespaces, _ := result["namespaces"].([]any)
	activeNS := creds.Namespace

	rows := make([][]string, 0, len(namespaces))
	for _, ns := range namespaces {
		nsMap, _ := ns.(map[string]any)
		name, _ := nsMap["name"].(string)
		status, _ := nsMap["cluster_status"].(string)

		active := ""
		if name == activeNS {
			active = "yes"
		}
		rows = append(rows, []string{name, status, active})
	}

	if len(rows) == 0 && !out.JSONMode() {
		fmt.Println("No namespaces found.")
		return nil
	}
	return out.Table([]string{"NAME", "CLUSTER", "ACTIVE"}, rows)
}

// --- WebRTC ----------------------------------------------------------------

// namespaceFeatures are the features enable and disable accept.
var namespaceFeatures = []string{"webrtc", "webrtc-stealth"}

// NamespaceEnable turns on a namespace feature.
func NamespaceEnable(feature, ns string) error {
	switch feature {
	case "webrtc-stealth":
		return namespaceStealthToggle(ns, true)
	case "webrtc":
		return namespaceWebRTCToggle(ns, true)
	default:
		return clierr.Usage("unknown feature %q; supported: %s",
			feature, strings.Join(namespaceFeatures, ", "))
	}
}

// NamespaceDisable turns off a namespace feature.
func NamespaceDisable(feature, ns string) error {
	switch feature {
	case "webrtc-stealth":
		return namespaceStealthToggle(ns, false)
	case "webrtc":
		return namespaceWebRTCToggle(ns, false)
	default:
		return clierr.Usage("unknown feature %q; supported: %s",
			feature, strings.Join(namespaceFeatures, ", "))
	}
}

// namespaceWebRTCToggle drives /v1/namespace/webrtc/{enable,disable}.
func namespaceWebRTCToggle(ns string, enable bool) error {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	if ns == "" {
		return clierr.Usage("--namespace is required: orama namespace %s webrtc --namespace <name>", verb)
	}

	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	if enable {
		fmt.Printf("Enabling WebRTC for namespace '%s'...\n", ns)
		fmt.Printf("This will provision SFU (3 nodes) and TURN (2 nodes) services.\n")
	} else {
		fmt.Printf("Disabling WebRTC for namespace '%s'...\n", ns)
	}

	if _, err := nsRequest(verb+" WebRTC", http.MethodPost,
		fmt.Sprintf("%s/v1/namespace/webrtc/%s", gatewayURL, verb), token, nil); err != nil {
		return err
	}

	if enable {
		fmt.Printf("WebRTC enabled for namespace '%s'.\n", ns)
		fmt.Printf("  SFU instances: 3 nodes (signaling via WireGuard)\n")
		fmt.Printf("  TURN instances: 2 nodes (relay on public IPs)\n")
	} else {
		fmt.Printf("WebRTC disabled for namespace '%s'.\n", ns)
		fmt.Printf("  SFU and TURN services stopped, ports deallocated, DNS records removed.\n")
	}
	return nil
}

// namespaceStealthToggle drives /v1/namespace/webrtc/stealth/{enable,disable}
// (feat-124 — censorship-resistant TURNS over :443).
func namespaceStealthToggle(ns string, enable bool) error {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	if ns == "" {
		return clierr.Usage("--namespace is required: orama namespace %s webrtc-stealth --namespace <name>", verb)
	}

	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	if enable {
		fmt.Printf("Enabling WebRTC stealth (TURNS over :443) for namespace '%s'...\n", ns)
		fmt.Printf("This provisions a Let's Encrypt cert for the neutral stealth host and may take up to ~2 minutes.\n")
	} else {
		fmt.Printf("Disabling WebRTC stealth for namespace '%s'...\n", ns)
	}

	if _, err := nsRequest(verb+" WebRTC stealth", http.MethodPost,
		fmt.Sprintf("%s/v1/namespace/webrtc/stealth/%s", gatewayURL, verb), token, nil); err != nil {
		return err
	}

	if enable {
		fmt.Printf("WebRTC stealth enabled for namespace '%s'.\n", ns)
		fmt.Printf("  turn.credentials now advertises the full URI ladder including turns:<stealth-host>:443.\n")
		fmt.Printf("  Make sure the SNI router is enabled on the TURN nodes (node.yaml sni_router.enabled).\n")
	} else {
		fmt.Printf("WebRTC stealth disabled for namespace '%s'.\n", ns)
	}
	return nil
}

// NamespaceWebRTCStatus reports a namespace's WebRTC configuration.
func NamespaceWebRTCStatus(ns string) error {
	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	result, err := nsRequest("read the WebRTC status", http.MethodGet,
		gatewayURL+"/v1/namespace/webrtc/status", token, nil)
	if err != nil {
		return err
	}

	enabled, _ := result["enabled"].(bool)
	if !enabled {
		fmt.Printf("WebRTC is not enabled for namespace '%s'.\n", ns)
		fmt.Printf("  Enable with: orama namespace enable webrtc --namespace %s\n", ns)
		return nil
	}

	fmt.Printf("WebRTC status for namespace '%s'\n\n", ns)
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
	return nil
}

// loadAuthForNamespace resolves the gateway and the API key to call it with.
func loadAuthForNamespace(ns string) (gatewayURL, token string, err error) {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", "", clierr.Failure("failed to load credentials: %w", err)
	}

	gatewayURL, err = getGatewayURL()
	if err != nil {
		return "", "", err
	}

	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		return "", "", clierr.Auth("not authenticated for %s: run 'orama auth login'", gatewayURL)
	}
	// A short-lived token, not the key. Key management is the last place to
	// send a key on every request.
	token, err = auth.Bearer(gatewayURL, store, creds)
	if err != nil {
		return "", "", clierr.Auth("%w", err)
	}
	return gatewayURL, token, nil
}

// NamespaceCreate creates a namespace and starts its cluster.
//
// This used to happen by itself: `orama auth login --namespace X` asked for a
// login challenge, and asking for a challenge created X. So a typo made a
// namespace, and creating one cost nothing and belonged to nobody in
// particular. It is a deliberate act now, and the wallet that makes it owns it.
func NamespaceCreate(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return clierr.Usage("a namespace name is required: orama namespace create <name>")
	}

	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}

	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil || !creds.IsValid() {
		return clierr.Auth("not authenticated: run 'orama auth login'")
	}

	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return clierr.Failure("failed to encode the request: %w", err)
	}

	result, err := nsRequest("create namespace", http.MethodPost,
		gatewayURL+"/v1/namespaces", creds.APIKey, bytes.NewReader(body))
	if err != nil {
		return err
	}

	status, _ := result["status"].(string)
	switch status {
	case "provisioning":
		fmt.Printf("Namespace %s created; its cluster is being provisioned.\n", name)
		fmt.Printf("Watch it with: orama namespace list\n")
		fmt.Printf("Then sign in to it: orama auth login --namespace %s\n", name)
	default:
		fmt.Printf("Namespace %s created.\n", name)
		if reason, ok := result["cluster"].(string); ok && reason != "" {
			fmt.Printf("Its cluster was not started: %s\n", reason)
		}
		fmt.Printf("Sign in to it with: orama auth login --namespace %s\n", name)
	}
	return nil
}

// clusterSecretPath is where a node keeps the cluster secret. The commands that
// talk to a node's own gateway over loopback run on that node, so it is here.
func clusterSecretPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}
	return filepath.Join(home, ".orama", "secrets", "cluster-secret")
}

// signCoordinationRequest stamps a node-to-node coordination request.
func signCoordinationRequest(r *http.Request) error {
	path := clusterSecretPath()
	secret, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read the cluster secret at %s: this command talks to the node's "+
			"own gateway and has to prove it is running on a node — run it on one: %w", path, err)
	}
	key, err := auth.CoordinationKey(string(secret))
	if err != nil {
		return err
	}
	return auth.SignCoordination(key, r, time.Now())
}
