package deployments

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/DeBrosOfficial/network/pkg/cli/shared"
	"github.com/spf13/cobra"
)

// EnvCmd is `orama app env`.
//
// The platform tells people to put secrets in environment variables. Until now
// they could only be set at deploy time, and only for Go apps, so changing one
// meant rebuilding and redeploying the whole application.
var EnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage an app's environment variables",
	Long: `Read and change the environment variables a deployed app runs with.

Setting or removing a variable restarts the app so it picks up the change.

Values are never printed back. They are where secrets live, so 'list' shows
names only.`,
}

var (
	envSetPairs []string
	envSetFile  string
	envJSON     bool
)

func init() {
	list := &cobra.Command{
		Use:   "list <app>",
		Short: "List an app's environment variable names",
		Args:  cobra.ExactArgs(1),
		RunE:  runEnvList,
	}
	list.Flags().BoolVar(&envJSON, "json", false, "Print the gateway's reply as JSON")

	set := &cobra.Command{
		Use:   "set <app>",
		Short: "Set environment variables and restart the app",
		Long: `Set one or more variables and restart the app.

Values given with --env never appear in shell history if you read them from a
file instead: --env-file takes a .env and sends every variable in it.`,
		Example: `  orama app env set my-api --env DATABASE_URL=postgres://...
  orama app env set my-api --env-file .env.production`,
		Args: cobra.ExactArgs(1),
		RunE: runEnvSet,
	}
	set.Flags().StringArrayVar(&envSetPairs, "env", nil, "Variable as KEY=VALUE (repeatable)")
	set.Flags().StringVar(&envSetFile, "env-file", "", "Read variables from a .env file")

	unset := &cobra.Command{
		Use:   "unset <app> <KEY>...",
		Short: "Remove environment variables and restart the app",
		Args:  cobra.MinimumNArgs(2),
		RunE:  runEnvUnset,
	}

	EnvCmd.AddCommand(list, set, unset)
}

func runEnvList(cmd *cobra.Command, args []string) error {
	raw, err := shared.Request("GET", "/v1/deployments/env?name="+url.QueryEscape(args[0]), nil)
	if err != nil {
		return err
	}
	if envJSON {
		fmt.Println(string(raw))
		return nil
	}

	var resp struct {
		Variables []struct {
			Key      string `json:"key"`
			Reserved bool   `json:"reserved"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse gateway response: %w", err)
	}
	if len(resp.Variables) == 0 {
		fmt.Printf("%s has no environment variables.\n", args[0])
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tSET BY\n")
	for _, v := range resp.Variables {
		setBy := "you"
		if v.Reserved {
			setBy = "the platform"
		}
		fmt.Fprintf(w, "%s\t%s\n", v.Key, setBy)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	fmt.Printf("\nValues are not shown. Set one again to change it.\n")
	return nil
}

func runEnvSet(cmd *cobra.Command, args []string) error {
	env, err := envPairs(envSetPairs, envSetFile)
	if err != nil {
		return err
	}
	if len(env) == 0 {
		return fmt.Errorf("nothing to set: give --env KEY=VALUE or --env-file")
	}
	return applyEnv(args[0], map[string]any{"set": env}, sortedEnvKeys(env), "set")
}

func runEnvUnset(cmd *cobra.Command, args []string) error {
	keys := args[1:]
	sort.Strings(keys)
	return applyEnv(args[0], map[string]any{"unset": keys}, keys, "removed")
}

// applyEnv sends one change and reports what happened to the app.
func applyEnv(app string, body map[string]any, keys []string, verb string) error {
	raw, err := shared.Request("POST", "/v1/deployments/env/set?name="+url.QueryEscape(app), body)
	if err != nil {
		return err
	}

	var resp struct {
		Restarted bool `json:"restarted"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse gateway response: %w", err)
	}

	for _, key := range keys {
		fmt.Printf("  %s %s\n", verb, key)
	}
	if resp.Restarted {
		fmt.Printf("\n✓ %s restarted with the new environment.\n", app)
	} else {
		// A static site has no process to restart.
		fmt.Printf("\n✓ %s updated. It has no running process, so nothing was restarted.\n", app)
	}
	return nil
}
