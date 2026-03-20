package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
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
func HandleInspectCommand(args []string) {
	// Load .env file from current directory (only sets unset vars)
	loadDotEnv(".env")

	fs := flag.NewFlagSet("inspect", flag.ExitOnError)

	configPath := fs.String("config", "scripts/nodes.conf", "Path to nodes.conf")
	env := fs.String("env", "", "Environment to inspect (devnet, testnet)")
	subsystem := fs.String("subsystem", "all", "Subsystem to inspect (rqlite,olric,ipfs,dns,wg,system,network,anyone,all)")
	format := fs.String("format", "table", "Output format (table, json)")
	timeout := fs.Duration("timeout", 30*time.Second, "SSH command timeout")
	verbose := fs.Bool("verbose", false, "Verbose output")
	// Output flags
	outputDir := fs.String("output", "", "Save results to directory as markdown (e.g., ./results)")
	// AI flags
	aiEnabled := fs.Bool("ai", false, "Enable AI analysis of failures")
	aiModel := fs.String("model", "moonshotai/kimi-k2.5", "OpenRouter model for AI analysis")
	aiAPIKey := fs.String("api-key", "", "OpenRouter API key (or OPENROUTER_API_KEY env)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: orama inspect [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Inspect cluster health by SSHing into nodes and running checks.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  orama inspect --env devnet\n")
		fmt.Fprintf(os.Stderr, "  orama inspect --env devnet --subsystem rqlite\n")
		fmt.Fprintf(os.Stderr, "  orama inspect --env devnet --ai\n")
		fmt.Fprintf(os.Stderr, "  orama inspect --env devnet --ai --model openai/gpt-4o\n")
		fmt.Fprintf(os.Stderr, "  orama inspect --env devnet --ai --output ./results\n")
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *env == "" {
		fmt.Fprintf(os.Stderr, "Error: --env is required (devnet, testnet)\n")
		os.Exit(1)
	}

	// Load nodes
	nodes, err := inspector.LoadNodes(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Filter by environment
	nodes = inspector.FilterByEnv(nodes, *env)
	if len(nodes) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no nodes found for environment %q\n", *env)
		os.Exit(1)
	}

	// Prepare wallet-derived SSH keys
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error preparing SSH keys: %v\n", err)
		os.Exit(1)
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

	// Exit with non-zero if any failures
	if failures := results.Failures(); len(failures) > 0 {
		os.Exit(1)
	}
}
