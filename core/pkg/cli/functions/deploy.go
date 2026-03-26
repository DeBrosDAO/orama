package functions

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// DeployCmd deploys a function to the Orama Network.
var DeployCmd = &cobra.Command{
	Use:   "deploy [directory]",
	Short: "Deploy a function to the Orama Network",
	Long: `Deploys the function in the given directory (or current directory).
If no .wasm file exists, it will be built automatically using TinyGo.
Reads configuration from function.yaml.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDeploy,
}

func runDeploy(cmd *cobra.Command, args []string) error {
	dir := ""
	if len(args) > 0 {
		dir = args[0]
	}

	absDir, err := ResolveFunctionDir(dir)
	if err != nil {
		return err
	}

	// Load configuration
	cfg, err := LoadConfig(absDir)
	if err != nil {
		return err
	}

	wasmPath := filepath.Join(absDir, "function.wasm")

	// Auto-build if no WASM file exists
	if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
		fmt.Printf("No function.wasm found, building...\n\n")
		built, err := buildFunction(dir)
		if err != nil {
			return err
		}
		wasmPath = built
		fmt.Println()
	} else {
		// Validate existing WASM
		if err := ValidateWASMFile(wasmPath); err != nil {
			return fmt.Errorf("existing function.wasm is invalid: %w\nRun 'orama function build' to rebuild", err)
		}
	}

	fmt.Printf("Deploying function %q...\n", cfg.Name)

	result, err := uploadWASMFunction(wasmPath, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("\nFunction deployed successfully!\n\n")

	if msg, ok := result["message"]; ok {
		fmt.Printf("  %s\n", msg)
	}
	if fn, ok := result["function"].(map[string]interface{}); ok {
		if id, ok := fn["id"]; ok {
			fmt.Printf("  ID:        %s\n", id)
		}
		fmt.Printf("  Name:      %s\n", cfg.Name)
		if v, ok := fn["version"]; ok {
			fmt.Printf("  Version:   %v\n", v)
		}
		if wc, ok := fn["wasm_cid"]; ok {
			fmt.Printf("  WASM CID:  %s\n", wc)
		}
		if st, ok := fn["status"]; ok {
			fmt.Printf("  Status:    %s\n", st)
		}
	}

	fmt.Printf("\nInvoke with:\n")
	fmt.Printf("  orama function invoke %s --data '{\"name\": \"World\"}'\n", cfg.Name)

	return nil
}
