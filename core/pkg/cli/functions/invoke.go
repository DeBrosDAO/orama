package functions

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

var invokeData string

// InvokeCmd invokes a deployed function.
var InvokeCmd = &cobra.Command{
	Use:   "invoke <name>",
	Short: "Invoke a deployed function",
	Long:  "Sends a request to invoke the named function with optional JSON payload.",
	Args:  cobra.ExactArgs(1),
	RunE:  runInvoke,
}

func init() {
	InvokeCmd.Flags().StringVar(&invokeData, "data", "{}", "JSON payload to send to the function")
}

func runInvoke(cmd *cobra.Command, args []string) error {
	name := args[0]

	fmt.Printf("Invoking function %q...\n\n", name)

	resp, err := apiRequest("POST", "/v1/functions/"+name+"/invoke", bytes.NewBufferString(invokeData), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Print timing info from headers
	if reqID := resp.Header.Get("X-Request-ID"); reqID != "" {
		fmt.Printf("Request ID:  %s\n", reqID)
	}
	if dur := resp.Header.Get("X-Duration-Ms"); dur != "" {
		fmt.Printf("Duration:    %s ms\n", dur)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invocation failed (%d): %s", resp.StatusCode, string(respBody))
	}

	fmt.Printf("\nOutput:\n%s\n", string(respBody))

	return nil
}
