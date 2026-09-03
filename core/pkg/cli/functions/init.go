package functions

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// InitCmd scaffolds a new function project.
var InitCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Create a new serverless function project",
	Long:  "Scaffolds a new directory with function.go and function.yaml templates.",
	Args:  cobra.ExactArgs(1),
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	name := args[0]

	if !validNameRegex.MatchString(name) {
		return fmt.Errorf("invalid function name %q: must start with a letter and contain only letters, digits, hyphens, or underscores", name)
	}

	dir := filepath.Join(".", name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("directory %q already exists", name)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write function.yaml
	yamlContent := fmt.Sprintf(`name: %s
public: false
memory: 64
timeout: 30
retry:
  count: 0
  delay: 5
`, name)

	if err := os.WriteFile(filepath.Join(dir, "function.yaml"), []byte(yamlContent), 0o644); err != nil {
		return fmt.Errorf("failed to write function.yaml: %w", err)
	}

	// Write function.go
	goContent := fmt.Sprintf(`package main

import "github.com/DeBrosOfficial/network/sdk/fn"

func main() {
	fn.Run(func(input []byte) ([]byte, error) {
		var req struct {
			Name string ` + "`" + `json:"name"` + "`" + `
		}
		fn.ParseJSON(input, &req)
		if req.Name == "" {
			req.Name = "World"
		}
		return fn.JSON(map[string]string{
			"greeting": "Hello, " + req.Name + "!",
		})
	})
}
`)

	if err := os.WriteFile(filepath.Join(dir, "function.go"), []byte(goContent), 0o644); err != nil {
		return fmt.Errorf("failed to write function.go: %w", err)
	}

	fmt.Printf("Created function project: %s/\n", name)
	fmt.Printf("  %s/function.yaml   — configuration\n", name)
	fmt.Printf("  %s/function.go     — handler code\n\n", name)
	fmt.Printf("Next steps:\n")
	fmt.Printf("  cd %s\n", name)
	fmt.Printf("  orama function build\n")
	fmt.Printf("  orama function deploy\n")

	return nil
}
