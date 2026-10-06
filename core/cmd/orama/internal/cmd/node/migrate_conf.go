package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
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

		// The gateway and the credential come first: with no login there is
		// nothing to register the nodes with, and that is the answer, whatever
		// the node inventory says.
		envConfig, err := cli.GetEnvironmentByName(env)
		if err != nil {
			return clierr.Usage("environment %q not configured (see 'orama env list'): %w", env, err)
		}
		token, err := shared.AuthToken(envConfig.GatewayURL)
		if err != nil {
			return err
		}

		nodes, err := noderesolver.ResolveNodes(env)
		if err != nil {
			return fmt.Errorf("failed to load nodes.conf: %w", err)
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
