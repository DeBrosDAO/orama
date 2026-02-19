package functions

import (
	"fmt"
	"os"
	"text/tabwriter"

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

	functions, ok := result["functions"].([]interface{})
	if !ok || len(functions) == 0 {
		fmt.Println("No functions deployed.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tSTATUS\tMEMORY\tTIMEOUT\tPUBLIC")
	fmt.Fprintln(w, "----\t-------\t------\t------\t-------\t------")

	for _, f := range functions {
		fn, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		name := valStr(fn, "name")
		version := valNum(fn, "version")
		status := valStr(fn, "status")
		memory := valNum(fn, "memory_limit_mb")
		timeout := valNum(fn, "timeout_seconds")
		public := valBool(fn, "is_public")

		publicStr := "no"
		if public {
			publicStr = "yes"
		}

		fmt.Fprintf(w, "%s\t%d\t%s\t%dMB\t%ds\t%s\n", name, version, status, memory, timeout, publicStr)
	}
	w.Flush()

	fmt.Printf("\nTotal: %d function(s)\n", len(functions))
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
