package cli

import (
	"bufio"
	"context"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	// Import checks package so init() registers the checkers
	_ "github.com/DeBrosOfficial/network/pkg/inspector/checks"
)

// loadDotEnv loads key=value pairs from a .env file into os environment.
// Only sets vars that are not already set (env takes precedence over file).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // .env is optional
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 1 {
			continue
		}
		key := line[:eq]
		value := line[eq+1:]
		// Only set if not already in environment
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

// HandleInspectCommand handles the "orama inspect" command.
// InspectOptions holds the flags for the inspect command.
type InspectOptions struct {
	ConfigPath string
	Env        string
	Subsystem  string
	Format     string
	Timeout    time.Duration
	Verbose    bool
	OutputDir  string
	// Nodes, when set, is the fleet to inspect. The command resolves them so
	// this package does not have to import the resolver (which imports it).
	Nodes     []inspector.Node
	AIEnabled bool
	AIModel   string
	AIAPIKey  string
}

// RunInspect inspects cluster health over SSH.
func RunInspect(opts InspectOptions) error {
	// Load .env file from current directory (only sets unset vars)
	loadDotEnv(".env")

	configPath := &opts.ConfigPath
	env := &opts.Env
	subsystem := &opts.Subsystem
	format := &opts.Format
	timeout := &opts.Timeout
	verbose := &opts.Verbose
	outputDir := &opts.OutputDir
	aiEnabled := &opts.AIEnabled
	aiModel := &opts.AIModel
	aiAPIKey := &opts.AIAPIKey

	if *env == "" {
		return clierr.Usage("--env is required (devnet, testnet)")
	}

	// Nodes come from the caller when it resolved them (the normal path), and
	// from an explicit --config file otherwise.
	nodes := opts.Nodes
	if len(nodes) == 0 {
		loaded, err := inspector.LoadNodes(*configPath)
		if err != nil {
			return fmt.Errorf("loading %s: %w", *configPath, err)
		}
		nodes = inspector.FilterByEnv(loaded, *env)
	}
	if len(nodes) == 0 {
		return clierr.NotFound("no nodes found for environment %q", *env)
	}

	// Prepare wallet-derived SSH keys
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return clierr.Failure("failed to prepare SSH keys: %w", err)
	}
	defer cleanup()

	// Parse subsystems
	var subsystems []string
	if *subsystem != "all" {
		subsystems = strings.Split(*subsystem, ",")
	}

	fmt.Printf("Inspecting %d %s nodes", len(nodes), *env)
	if len(subsystems) > 0 {
		fmt.Printf(" [%s]", strings.Join(subsystems, ","))
	}
	if *aiEnabled {
		fmt.Printf(" (AI: %s)", *aiModel)
	}
	fmt.Printf("...\n\n")

	// Phase 1: Collect
	ctx, cancel := context.WithTimeout(context.Background(), *timeout+10*time.Second)
	defer cancel()

	if *verbose {
		fmt.Printf("Collecting data from %d nodes (timeout: %s)...\n", len(nodes), timeout)
	}

	data := inspector.Collect(ctx, nodes, subsystems, *verbose)

	if *verbose {
		fmt.Printf("Collection complete in %.1fs\n\n", data.Duration.Seconds())
	}

	// Phase 2: Check
	results := inspector.RunChecks(data, subsystems)

	// Phase 3: Report
	switch *format {
	case "json":
		inspector.PrintJSON(results, os.Stdout)
	default:
		inspector.PrintTable(results, os.Stdout)
	}

	// Phase 4: AI Analysis (if enabled and there are failures or warnings)
	var analysis *inspector.AnalysisResult
	if *aiEnabled {
		issues := results.FailuresAndWarnings()
		if len(issues) == 0 {
			fmt.Printf("\nAll checks passed — no AI analysis needed.\n")
		} else if *outputDir != "" {
			// Per-group AI analysis for file output
			groups := inspector.GroupFailures(results)
			fmt.Printf("\nAnalyzing %d unique issues with %s...\n", len(groups), *aiModel)
			var err error
			analysis, err = inspector.AnalyzeGroups(groups, results, data, *aiModel, *aiAPIKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nAI analysis failed: %v\n", err)
			} else {
				inspector.PrintAnalysis(analysis, os.Stdout)
			}
		} else {
			// Per-subsystem AI analysis for terminal output
			subs := map[string]bool{}
			for _, c := range issues {
				subs[c.Subsystem] = true
			}
			fmt.Printf("\nAnalyzing %d issues across %d subsystems with %s...\n", len(issues), len(subs), *aiModel)
			var err error
			analysis, err = inspector.Analyze(results, data, *aiModel, *aiAPIKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nAI analysis failed: %v\n", err)
			} else {
				inspector.PrintAnalysis(analysis, os.Stdout)
			}
		}
	}

	// Phase 5: Write results to disk (if --output is set)
	if *outputDir != "" {
		outPath, err := inspector.WriteResults(*outputDir, *env, results, data, analysis)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError writing results: %v\n", err)
		} else {
			fmt.Printf("\nResults saved to %s\n", outPath)
		}
	}

	// A failed check is a failed command.
	if failures := results.Failures(); len(failures) > 0 {
		return fmt.Errorf("%d health check(s) failed", len(failures))
	}
	return nil
}
