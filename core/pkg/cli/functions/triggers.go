package functions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	triggerTopic    string
	triggerSchedule string
)

// TriggersCmd is the parent command for trigger management.
var TriggersCmd = &cobra.Command{
	Use:   "triggers",
	Short: "Manage function PubSub and cron triggers",
	Long: `Add, list, and delete triggers for your serverless functions.

PubSub: when a message is published to a topic, every function with a
matching trigger is invoked with the message as input.

Cron: a function is invoked on a schedule (5-field crontab, or 6-field
crontab with a leading seconds column).

Examples:
  orama function triggers add my-function --topic calls:invite
  orama function triggers add my-function --schedule "0 3 * * *"
  orama function triggers add my-function --schedule "*/30 * * * * *"
  orama function triggers list my-function
  orama function triggers delete my-function <trigger-id>`,
}

// TriggersAddCmd adds a PubSub or Cron trigger to a function.
var TriggersAddCmd = &cobra.Command{
	Use:   "add <function-name>",
	Short: "Add a PubSub or Cron trigger",
	Long: `Registers a trigger that invokes the function automatically.

Pass exactly one of --topic (PubSub) or --schedule (cron). Schedules
accept either 5-field crontab (minute hour dom month dow) or 6-field
with seconds (sec minute hour dom month dow).`,
	Args: cobra.ExactArgs(1),
	RunE: runTriggersAdd,
}

// TriggersListCmd lists triggers for a function.
var TriggersListCmd = &cobra.Command{
	Use:   "list <function-name>",
	Short: "List triggers for a function",
	Args:  cobra.ExactArgs(1),
	RunE:  runTriggersList,
}

// TriggersDeleteCmd deletes a trigger.
var TriggersDeleteCmd = &cobra.Command{
	Use:   "delete <function-name> <trigger-id>",
	Short: "Delete a trigger",
	Args:  cobra.ExactArgs(2),
	RunE:  runTriggersDelete,
}

func init() {
	TriggersCmd.AddCommand(TriggersAddCmd)
	TriggersCmd.AddCommand(TriggersListCmd)
	TriggersCmd.AddCommand(TriggersDeleteCmd)

	TriggersAddCmd.Flags().StringVar(&triggerTopic, "topic", "", "PubSub topic to trigger on")
	TriggersAddCmd.Flags().StringVar(&triggerSchedule, "schedule", "", "Cron expression to trigger on (e.g. \"0 3 * * *\")")
	TriggersAddCmd.MarkFlagsMutuallyExclusive("topic", "schedule")
	TriggersAddCmd.MarkFlagsOneRequired("topic", "schedule")
}

func runTriggersAdd(cmd *cobra.Command, args []string) error {
	funcName := args[0]

	body, _ := json.Marshal(map[string]string{
		"topic":           triggerTopic,
		"cron_expression": triggerSchedule,
	})

	resp, err := apiRequest("POST", "/v1/functions/"+funcName+"/triggers", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if triggerSchedule != "" {
		fmt.Printf("Trigger added: cron(%s) → %s (id: %s)\n", triggerSchedule, funcName, result["trigger_id"])
	} else {
		fmt.Printf("Trigger added: %s → %s (id: %s)\n", triggerTopic, funcName, result["trigger_id"])
	}
	return nil
}

func runTriggersList(cmd *cobra.Command, args []string) error {
	funcName := args[0]

	result, err := apiGet("/v1/functions/" + funcName + "/triggers")
	if err != nil {
		return err
	}

	triggers, _ := result["triggers"].([]interface{})
	if len(triggers) == 0 {
		fmt.Printf("No triggers for function %q.\n", funcName)
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTOPIC\tENABLED")
	for _, t := range triggers {
		tr, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := tr["ID"].(string)
		if id == "" {
			id, _ = tr["id"].(string)
		}
		topic, _ := tr["Topic"].(string)
		if topic == "" {
			topic, _ = tr["topic"].(string)
		}
		enabled := true
		if e, ok := tr["Enabled"].(bool); ok {
			enabled = e
		} else if e, ok := tr["enabled"].(bool); ok {
			enabled = e
		}
		fmt.Fprintf(w, "%s\t%s\t%v\n", id, topic, enabled)
	}
	w.Flush()
	return nil
}

func runTriggersDelete(cmd *cobra.Command, args []string) error {
	funcName := args[0]
	triggerID := args[1]

	result, err := apiDelete("/v1/functions/" + funcName + "/triggers/" + triggerID)
	if err != nil {
		return err
	}

	if msg, ok := result["message"]; ok {
		fmt.Println(msg)
	} else {
		fmt.Println("Trigger deleted.")
	}
	return nil
}
