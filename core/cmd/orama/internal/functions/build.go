package functions

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// tinygoBuildArgs returns the argv (without the leading `tinygo`) used
// to compile a function. Pure function — extracted from buildFunction
// so the WS-persistent → `-buildmode=c-shared` policy can be unit
// tested without invoking TinyGo.
//
// Persistent WS functions need the WASI-reactor variant (exports
// `_initialize`, no `_start`) — see the comment on cfg loading in
// buildFunction for the full rationale. Stateless (default) functions
// stay on command mode for back-compat.
func tinygoBuildArgs(outputPath string, cShared bool) []string {
	args := []string{"build", "-o", outputPath, "-target", "wasi"}
	if cShared {
		args = append(args, "-buildmode=c-shared")
	}
	args = append(args, ".")
	return args
}

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

	// Load config so we can pick the right TinyGo build mode based on
	// ws_persistent. Persistent functions need WASI-reactor semantics
	// (`_initialize` export, no `_start`); command-mode functions stay
	// on the default. See bug #240/#249 follow-up #6 for the full
	// rationale — TL;DR: TinyGo command-mode `_start` doesn't set the
	// runtime guard `wasmExportCheckRun` checks, so any export call
	// from the host (e.g. orama_alloc → ws_open payload) traps with
	// "wasm error: unreachable" inside the runtime hashmap path.
	//
	// `-buildmode=c-shared` flips TinyGo to reactor mode: the wasm
	// exports `_initialize` instead of `_start`. The gateway's
	// persistent-instance bootstrap (pkg/serverless/engine.go) calls
	// `_initialize` first if exported, which sets the guard cleanly,
	// and the function's exports become callable from the host loop.
	cfg, cfgErr := LoadConfig(absDir)
	if cfgErr != nil {
		return "", fmt.Errorf("failed to load function.yaml: %w", cfgErr)
	}

	// Check TinyGo is installed
	tinygoPath, err := exec.LookPath("tinygo")
	if err != nil {
		return "", fmt.Errorf("tinygo not found in PATH. Install it: https://tinygo.org/getting-started/install/")
	}

	outputPath := filepath.Join(absDir, "function.wasm")

	fmt.Printf("Building %s...\n", absDir)

	// Build args. Default = command mode. Persistent WS functions AND
	// reactor-mode stateless functions (bugboard #898) get `-buildmode=c-shared`
	// so TinyGo emits `_initialize` (WASI reactor) and the runtime drives the
	// exported entrypoints (ws_open/ws_frame/ws_close, or handle) explicitly.
	cShared := cfg.WSPersistent || cfg.Reactor
	tinygoArgs := tinygoBuildArgs(outputPath, cShared)
	if cfg.WSPersistent {
		fmt.Printf("  (ws_persistent=true → using -buildmode=c-shared for WASI-reactor semantics)\n")
	} else if cfg.Reactor {
		fmt.Printf("  (reactor=true → using -buildmode=c-shared; export handle(ptr,len)->packed)\n")
	}

	buildCmd := exec.Command(tinygoPath, tinygoArgs...)
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
