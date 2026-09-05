package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/spf13/cobra"
)

var migrateConfEnv string

var migrateConfCmd = &cobra.Command{
	Use:   "migrate-conf",
	Short: "Register nodes.conf nodes with your wallet",
	Long: `One-time migration: reads nodes from nodes.conf for an environment
and registers each with your wallet via the gateway API. After migration,
these nodes will appear in 'orama nodes' output.

Requires: orama auth login (for API authentication)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		env := migrateConfEnv
		if env == "" {
			active, err := cli.GetActiveEnvironment()
			if err != nil {
				return fmt.Errorf("failed to get active environment: %w", err)
			}
			env = active.Name
		}

		// Load nodes from nodes.conf
		nodes, err := noderesolver.ResolveNodes(env)
		if err != nil {
			return fmt.Errorf("failed to load nodes.conf: %w", err)
		}

		// Get gateway URL
		envConfig, err := cli.GetEnvironmentByName(env)
		if err != nil {
			return fmt.Errorf("environment %q not configured: %w", env, err)
		}

		// Load stored credentials
		store, err := auth.LoadEnhancedCredentials()
		if err != nil {
			return fmt.Errorf("failed to load credentials: %w", err)
		}
		creds := store.GetDefaultCredential(envConfig.GatewayURL)
		if creds == nil {
			return fmt.Errorf("no credentials for %s — run 'orama auth login' first", envConfig.GatewayURL)
		}
		token, err := auth.Bearer(envConfig.GatewayURL, store, creds)
		if err != nil {
			return err
		}

		if len(nodes) == 0 {
			fmt.Printf("No nodes found for environment %q in nodes.conf\n", env)
			return nil
		}

		fmt.Printf("Migrating %d node(s) from nodes.conf to %s...\n\n", len(nodes), env)

		httpClient := &http.Client{Timeout: 10 * time.Second}
		registered := 0

		for _, n := range nodes {
			body := map[string]string{
				"ip_address":  n.Host,
				"environment": env,
				"role":        n.Role,
				"ssh_user":    n.User,
			}
			payload, _ := json.Marshal(body)

			req, err := http.NewRequest(http.MethodPost,
				envConfig.GatewayURL+"/v1/operator/node/register",
				bytes.NewReader(payload))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s: failed to create request: %v\n", n.Host, err)
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)

			resp, err := httpClient.Do(req)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s: request failed: %v\n", n.Host, err)
				continue
			}
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				fmt.Printf("  %s (%s): registered\n", n.Host, n.Role)
				registered++
			} else if resp.StatusCode == http.StatusNotFound {
				fmt.Printf("  %s: not found in cluster (node may not have joined yet)\n", n.Host)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s: HTTP %d: %s\n", n.Host, resp.StatusCode, string(respBody))
			}
		}

		fmt.Printf("\n%d/%d nodes registered with your wallet\n", registered, len(nodes))
		if registered < len(nodes) {
			fmt.Println("Nodes not found may need to join the cluster first, then re-run this command.")
		}
		return nil
	},
}

func init() {
	migrateConfCmd.Flags().StringVar(&migrateConfEnv, "env", "", "Environment to migrate (default: active)")
}
