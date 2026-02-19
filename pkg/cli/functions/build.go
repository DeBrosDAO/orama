package functions

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// BuildCmd compiles a function to WASM using TinyGo.
var BuildCmd = &cobra.Command{
	Use:   "build [directory]",
	Short: "Build a function to WASM using TinyGo",
	Long: `Compiles function.go in the given directory (or current directory) to a WASM binary.
Requires TinyGo to be installed (https://tinygo.org/getting-started/install/).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runBuild,
}

func runBuild(cmd *cobra.Command, args []string) error {
	dir := ""
	if len(args) > 0 {
		dir = args[0]
	}
	_, err := buildFunction(dir)
	return err
}

// buildFunction compiles the function in dir and returns the path to the WASM output.
func buildFunction(dir string) (string, error) {
	absDir, err := ResolveFunctionDir(dir)
	if err != nil {
		return "", err
	}

	// Verify function.go exists
	goFile := filepath.Join(absDir, "function.go")
	if _, err := os.Stat(goFile); os.IsNotExist(err) {
		return "", fmt.Errorf("function.go not found in %s", absDir)
	}

	// Verify function.yaml exists
	if _, err := os.Stat(filepath.Join(absDir, "function.yaml")); os.IsNotExist(err) {
		return "", fmt.Errorf("function.yaml not found in %s", absDir)
	}

	// Check TinyGo is installed
	tinygoPath, err := exec.LookPath("tinygo")
	if err != nil {
		return "", fmt.Errorf("tinygo not found in PATH. Install it: https://tinygo.org/getting-started/install/")
	}

	outputPath := filepath.Join(absDir, "function.wasm")

	fmt.Printf("Building %s...\n", absDir)

	// Run tinygo build
	buildCmd := exec.Command(tinygoPath, "build", "-o", outputPath, "-target", "wasi", ".")
	buildCmd.Dir = absDir
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr

	if err := buildCmd.Run(); err != nil {
		return "", fmt.Errorf("tinygo build failed: %w", err)
	}

	// Validate output
	if err := ValidateWASMFile(outputPath); err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("build produced invalid WASM: %w", err)
	}

	info, _ := os.Stat(outputPath)
	fmt.Printf("Built %s (%d bytes)\n", outputPath, info.Size())

	return outputPath, nil
}
