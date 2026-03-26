package functions

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// GetCmd shows details of a deployed function.
var GetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get details of a deployed function",
	Long:  "Retrieves and displays detailed information about a specific function.",
	Args:  cobra.ExactArgs(1),
	RunE:  runGet,
}

func runGet(cmd *cobra.Command, args []string) error {
	name := args[0]

	result, err := apiGet("/v1/functions/" + name)
	if err != nil {
		return err
	}

	// Pretty-print the result
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to format response: %w", err)
	}

	fmt.Println(string(data))
	return nil
}
