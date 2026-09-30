package functions

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/sdk"
	"github.com/spf13/cobra"
)

// InitCmd scaffolds a new function project.
var InitCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Create a new serverless function project",
	Long:  "Scaffolds a new directory with function.go, function.yaml, go.mod and a copy of the function SDK, ready for 'orama function build'.",
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

	// go.mod: TinyGo builds a module, and refuses a directory without one.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(scaffoldGoMod(name)), 0o644); err != nil {
		return fmt.Errorf("failed to write go.mod: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "function.go"), []byte(scaffoldHandler(name)), 0o644); err != nil {
		return fmt.Errorf("failed to write function.go: %w", err)
	}

	// The SDK is copied into the project. Its import path in this repository
	// is not one a function's module can fetch, and a copy builds with
	// nothing downloaded.
	sdkDir := filepath.Join(dir, "fn")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "fn.go"), []byte(vendoredSDK), 0o644); err != nil {
		return fmt.Errorf("failed to write fn/fn.go: %w", err)
	}

	fmt.Printf("Created function project: %s/\n", name)
	fmt.Printf("  %s/function.yaml   — configuration\n", name)
	fmt.Printf("  %s/function.go     — handler code\n", name)
	fmt.Printf("  %s/go.mod          — Go module\n", name)
	fmt.Printf("  %s/fn/fn.go        — the function SDK, copied in so the build fetches nothing\n\n", name)
	fmt.Printf("Next steps:\n")
	fmt.Printf("  cd %s\n", name)
	fmt.Printf("  orama function build\n")
	fmt.Printf("  orama function deploy\n")

	return nil
}

// scaffoldGoVersion is the go directive of a scaffolded module. It is the
// oldest release with the language features a handler needs, which every
// toolchain TinyGo runs on accepts: TinyGo refuses a module whose go directive
// is newer than the Go it was built with, so tracking this repository's own
// version would break the build on the next TinyGo release that lags it. It is
// the version every other WASM app in this repository's tests declares.
const scaffoldGoVersion = "1.22"

// scaffoldGoMod is the go.mod of a new function named name. The module is
// named after the function; the name only has to be a valid module path, which
// a function name (letters, digits, hyphens, underscores) always is.
func scaffoldGoMod(name string) string {
	return fmt.Sprintf("module %s\n\ngo %s\n", name, scaffoldGoVersion)
}

// vendoredSDK is fn/fn.go of a new function: the function SDK, with a note of
// where it came from.
var vendoredSDK = "// Copied by 'orama function init' from " + sdkImportPath + ".\n" +
	"// It is part of this project now: edit it, or replace it with your own helpers.\n\n" +
	sdk.FnSource

// sdkImportPath is where the SDK lives in the Orama repository.
const sdkImportPath = "github.com/DeBrosOfficial/network/sdk/fn"

// scaffoldHandler is the function.go of a new function named name, importing
// the SDK copied into the project.
func scaffoldHandler(name string) string {
	return `package main

import "` + name + `/fn"

func main() {
	fn.Run(func(input []byte) ([]byte, error) {
		var req struct {
			Name string ` + "`json:\"name\"`" + `
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
`
}
