package functions

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"github.com/spf13/cobra"
)

// ListCmd lists all deployed functions.
var ListCmd = &cobra.Command{
	Use:   "list",
	Short: "List deployed functions",
	Long:  "Lists all functions deployed in the current namespace.",
	Args:  cobra.NoArgs,
	RunE:  runList,
}

func runList(cmd *cobra.Command, args []string) error {
	result, err := apiGet("/v1/functions")
	if err != nil {
		return err
	}

	out := printer.For(cmd)
	functions, _ := result["functions"].([]interface{})

	if len(functions) == 0 && !out.JSONMode() {
		out.Printf("No functions deployed.\n")
		return nil
	}

	rows := make([][]string, 0, len(functions))
	for _, f := range functions {
		fn, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		publicStr := "no"
		if valBool(fn, "is_public") {
			publicStr = "yes"
		}
		rows = append(rows, []string{
			valStr(fn, "name"),
			fmt.Sprintf("%d", valNum(fn, "version")),
			valStr(fn, "status"),
			fmt.Sprintf("%dMB", valNum(fn, "memory_limit_mb")),
			fmt.Sprintf("%ds", valNum(fn, "timeout_seconds")),
			publicStr,
		})
	}

	if err := out.Table([]string{"NAME", "VERSION", "STATUS", "MEMORY", "TIMEOUT", "PUBLIC"}, rows); err != nil {
		return err
	}
	out.Printf("\nTotal: %d function(s)\n", len(functions))
	return nil
}

func valStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func valNum(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func valBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}
