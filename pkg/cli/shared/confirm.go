package shared

import (
	"bufio"
	"fmt"
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

// RequireRoot exits with an error if the current user is not root.
func RequireRoot() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "Error: This command must be run as root (use sudo)\n")
		os.Exit(1)
	}
}
