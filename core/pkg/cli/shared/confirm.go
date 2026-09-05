package shared

import (
	"bufio"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"os"
	"strings"
)

// Confirm prompts the user for yes/no confirmation. Returns true if user confirms.
func Confirm(prompt string) bool {
	fmt.Printf("%s (y/N): ", prompt)
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

// ConfirmExact prompts the user to type an exact string to confirm. Returns true if matched.
func ConfirmExact(prompt, expected string) bool {
	fmt.Printf("%s: ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	return strings.TrimSpace(scanner.Text()) == expected
}

// RequireRoot returns an error if the current user is not root.
//
// It delegates to clierr so the message and the exit code are the same
// wherever the check is made.
func RequireRoot() error {
	return clierr.RequireRoot("this command")
}
