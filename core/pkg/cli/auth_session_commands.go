package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// The commands that ask the gateway rather than the credentials file.
//
// `whoami` and `status` read ~/.orama/credentials.json and reported a revoked
// key as authenticated, because the file has no idea. `logout` deleted the file
// and left the session live on the server, so logging out of a machine you were
// worried about changed nothing about what that machine could still do.

// AuthWhoamiOnline asks the gateway who this credential is and what it may do.
func AuthWhoamiOnline() error {
	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}
	token, err := currentBearer(gatewayURL)
	if err != nil {
		return err
	}

	body, status, err := authRequest(http.MethodGet, gatewayURL+"/v1/auth/whoami", token, nil)
	if err != nil {
		return clierr.Failure("%w", err)
	}
	if status != http.StatusOK {
		return clierr.Auth("%w", auth.GatewayErrorFrom(status, body))
	}

	var out struct {
		Authenticated bool     `json:"authenticated"`
		Method        string   `json:"method"`
		Principal     string   `json:"principal"`
		Subject       string   `json:"subject"`
		Namespace     string   `json:"namespace"`
		Role          *string  `json:"role"`
		Grants        []string `json:"grants"`
		Resource      string   `json:"resource"`
		ExpiresAt     int64    `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return clierr.Failure("could not read the gateway's answer: %w", err)
	}
	if !out.Authenticated {
		return clierr.Auth("the gateway does not recognise this credential: run 'orama auth login'")
	}

	fmt.Printf("Authenticated with %s\n", gatewayURL)
	fmt.Printf("  principal:  %s\n", or(out.Principal, "unknown"))
	fmt.Printf("  subject:    %s\n", out.Subject)
	fmt.Printf("  namespace:  %s\n", out.Namespace)
	fmt.Printf("  credential: %s\n", or(out.Method, "unknown"))
	if out.Role != nil {
		fmt.Printf("  role:       %s\n", *out.Role)
	} else {
		fmt.Printf("  role:       none — this principal holds no grant in %s\n", out.Namespace)
	}
	if len(out.Grants) > 0 {
		sort.Strings(out.Grants)
		fmt.Printf("  grants:     %s\n", strings.Join(out.Grants, ", "))
	}
	if out.Resource != "" {
		fmt.Printf("  limited to: %s\n", out.Resource)
	}
	if out.ExpiresAt > 0 {
		fmt.Printf("  expires:    %s\n", time.Unix(out.ExpiresAt, 0).Local().Format("2006-01-02 15:04:05"))
	}
	return nil
}

// AuthLogoutOnline ends the session on the gateway before clearing it here.
func AuthLogoutOnline(all bool) error {
	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}

	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}
	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		fmt.Println("No credentials stored for this gateway. Nothing to log out of.")
		return nil
	}

	serverSide := endSessionOnGateway(gatewayURL, store, creds, all)

	if err := auth.ClearAllCredentials(); err != nil {
		return clierr.Failure("failed to clear credentials: %w", err)
	}

	// The local file is gone either way — that is what was asked for — but a
	// session that is still live on the server is the thing somebody logging
	// out of a machine they are worried about needs to know about.
	if serverSide != nil {
		return clierr.Failure("the local credentials are cleared, but the session is still live on the gateway: %w.\n"+
			"  End it from a machine that is still signed in: orama auth sessions revoke --all", serverSide)
	}

	fmt.Println("Logged out. The session is ended on the gateway and the local credentials are cleared.")
	return nil
}

// endSessionOnGateway revokes the refresh token, and every session when asked.
func endSessionOnGateway(gatewayURL string, store *auth.EnhancedCredentialStore, creds *auth.Credentials, all bool) error {
	payload, err := json.Marshal(map[string]any{
		"refresh_token": creds.RefreshToken,
		"namespace":     creds.Namespace,
		"all":           all,
	})
	if err != nil {
		return fmt.Errorf("encode the request: %w", err)
	}

	// Revoking every session needs to say who is asking; revoking one needs
	// only the token being revoked, which is already in the body.
	token := ""
	if all {
		if token, err = auth.Bearer(gatewayURL, store, creds); err != nil {
			return err
		}
	}

	body, status, err := authRequest(http.MethodPost, gatewayURL+"/v1/auth/logout", token, payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return auth.GatewayErrorFrom(status, body)
	}
	return nil
}

// AuthSessionsList shows which machines are signed in as this wallet.
func AuthSessionsList() error {
	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}
	token, err := currentBearer(gatewayURL)
	if err != nil {
		return err
	}

	body, status, err := authRequest(http.MethodGet, gatewayURL+"/v1/auth/sessions", token, nil)
	if err != nil {
		return clierr.Failure("%w", err)
	}
	if status != http.StatusOK {
		return clierr.Auth("%w", auth.GatewayErrorFrom(status, body))
	}

	var out struct {
		Namespace string `json:"namespace"`
		Subject   string `json:"subject"`
		Sessions  []struct {
			ID        int64  `json:"id"`
			CreatedAt string `json:"created_at"`
			ExpiresAt string `json:"expires_at"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return clierr.Failure("could not read the gateway's answer: %w", err)
	}

	if len(out.Sessions) == 0 {
		fmt.Printf("No live sessions for %s in namespace %s.\n", out.Subject, out.Namespace)
		return nil
	}

	fmt.Printf("Sessions for %s in namespace %s (%d):\n\n", out.Subject, out.Namespace, len(out.Sessions))
	fmt.Printf("  %-8s  %-25s  %s\n", "ID", "SIGNED IN", "EXPIRES")
	for _, session := range out.Sessions {
		fmt.Printf("  %-8d  %-25s  %s\n", session.ID, or(session.CreatedAt, "-"), or(session.ExpiresAt, "-"))
	}
	fmt.Println("\n  End one with 'orama auth sessions revoke <id>', or all of them with --all.")
	return nil
}

// AuthSessionsRevoke ends one session, or every one.
func AuthSessionsRevoke(id int64, all bool) error {
	if all {
		return AuthLogoutOnline(true)
	}
	if id <= 0 {
		return clierr.Usage("which session: orama auth sessions revoke <id>, as listed by 'orama auth sessions'")
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}
	token, err := currentBearer(gatewayURL)
	if err != nil {
		return err
	}

	body, status, err := authRequest(http.MethodDelete,
		fmt.Sprintf("%s/v1/auth/sessions/%d", gatewayURL, id), token, nil)
	if err != nil {
		return clierr.Failure("%w", err)
	}
	if status != http.StatusOK {
		return clierr.Failure("%w", auth.GatewayErrorFrom(status, body))
	}

	fmt.Printf("Session %d ended.\n", id)
	fmt.Println("  An access token already minted from it keeps working until it expires, at most 15 minutes.")
	return nil
}

// currentBearer is the credential this machine holds for a gateway.
func currentBearer(gatewayURL string) (string, error) {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", clierr.Failure("failed to load credentials: %w", err)
	}
	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		return "", clierr.Auth("not authenticated for %s: run 'orama auth login'", gatewayURL)
	}
	token, err := auth.Bearer(gatewayURL, store, creds)
	if err != nil {
		return "", clierr.Auth("%w", err)
	}
	return token, nil
}

// authRequest is one call to an auth endpoint, returning the body whatever the
// status: every one of these endpoints says something in a non-200, and
// throwing it away is how "the gateway refused" became the whole of what the
// CLI could tell you.
func authRequest(method, url, token string, payload []byte) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		body = strings.NewReader(string(payload))
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, 0, fmt.Errorf("build the request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := tlsutil.NewHTTPClientForDomain(30*time.Second, domainOf(url))
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("reach %s: %w", url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read the response: %w", err)
	}
	return raw, resp.StatusCode, nil
}

// domainOf is the host a URL names.
func domainOf(url string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	host, _, _ := strings.Cut(trimmed, "/")
	host, _, _ = strings.Cut(host, ":")
	return host
}

// or is the first non-empty of two strings.
func or(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
