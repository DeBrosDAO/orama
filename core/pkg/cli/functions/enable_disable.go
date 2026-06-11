package functions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

// DisableCmd pauses a function without redeploying.
//
// Plan 11.5 — operators flip a function's status during incident
// response, then re-enable when fixed. Existing in-flight invocations
// finish; new ones return 503 because the invoker treats inactive
// functions as missing.
var DisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable a function without deleting it",
	Long: `Disables a deployed function. The function row stays in the registry but
new invocations are rejected. Use 'orama function enable' to resume.

Useful during incident response — pause a misbehaving function until you
can root-cause without losing its deployed code or version history.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetEnabled(args[0], false)
	},
}

// EnableCmd resumes a disabled function. Inverse of DisableCmd.
var EnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Re-enable a previously disabled function",
	Long:  `Re-enables a function that was paused with 'orama function disable'.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetEnabled(args[0], true)
	},
}

func runSetEnabled(name string, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	resp, err := apiPostNoBody("/v1/functions/" + name + "/" + action)
	if err != nil {
		return err
	}
	verb := "disabled"
	if enabled {
		verb = "enabled"
	}
	if msg, ok := resp["message"]; ok {
		fmt.Println(msg)
	} else {
		fmt.Printf("Function %q %s.\n", name, verb)
	}
	return nil
}

// apiPostNoBody performs an authenticated POST with no body. Used by
// the disable/enable endpoints which take no payload (action is in the
// URL path).
func apiPostNoBody(endpoint string) (map[string]interface{}, error) {
	resp, err := apiRequest(http.MethodPost, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return result, nil
}
