package functions

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

var logsLimit int

// LogsCmd retrieves function execution logs.
var LogsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Get execution logs for a function",
	Long:  "Retrieves the most recent execution logs for a deployed function.",
	Args:  cobra.ExactArgs(1),
	RunE:  runLogs,
}

func init() {
	LogsCmd.Flags().IntVar(&logsLimit, "limit", 50, "Maximum number of log entries to retrieve")
}

func runLogs(cmd *cobra.Command, args []string) error {
	name := args[0]

	endpoint := "/v1/functions/" + name + "/logs"
	if logsLimit > 0 {
		endpoint += "?limit=" + strconv.Itoa(logsLimit)
	}

	result, err := apiGet(endpoint)
	if err != nil {
		return err
	}

	logs, ok := result["logs"].([]interface{})
	if !ok || len(logs) == 0 {
		fmt.Printf("No logs found for function %q.\n", name)
		return nil
	}

	for _, entry := range logs {
		log, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		ts := valStr(log, "timestamp")
		level := valStr(log, "level")
		msg := valStr(log, "message")
		fmt.Printf("[%s] %s: %s\n", ts, level, msg)
	}

	fmt.Printf("\nShowing %d log(s)\n", len(logs))
	return nil
}
