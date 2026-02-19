package functions

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var deleteForce bool

// DeleteCmd deletes a deployed function.
var DeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a deployed function",
	Long:  "Deletes a function from the Orama Network. This action cannot be undone.",
	Args:  cobra.ExactArgs(1),
	RunE:  runDelete,
}

func init() {
	DeleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Skip confirmation prompt")
}

func runDelete(cmd *cobra.Command, args []string) error {
	name := args[0]

	if !deleteForce {
		fmt.Printf("Are you sure you want to delete function %q? This cannot be undone. [y/N] ", name)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	result, err := apiDelete("/v1/functions/" + name)
	if err != nil {
		return err
	}

	if msg, ok := result["message"]; ok {
		fmt.Println(msg)
	} else {
		fmt.Printf("Function %q deleted.\n", name)
	}

	return nil
}
