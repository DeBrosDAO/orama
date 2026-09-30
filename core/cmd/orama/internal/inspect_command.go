package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
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

// inspectFormats are the --format values.
var inspectFormats = []string{"table", "json"}

// Validate refuses options that cannot work, before any node is asked
// anything: a mistake on the command line is exit 2, not an inspection that
// runs and reports on nothing.
func (o InspectOptions) Validate() error {
	if o.Env == "" {
		return clierr.Usage("--env is required (devnet, testnet)")
	}
	if !slices.Contains(inspectFormats, o.Format) {
		return clierr.Usage("unknown --format %q (one of: %s)", o.Format, strings.Join(inspectFormats, ", "))
	}
	if o.Timeout <= 0 {
		return clierr.Usage("--timeout must be positive, got %s", o.Timeout)
	}
	if err := inspector.ValidateSubsystems(o.subsystems()); err != nil {
		return clierr.Usage("--subsystem: %w", err)
	}
	return nil
}

// subsystems is the --subsystem list, or nil for all of them.
func (o InspectOptions) subsystems() []string {
	if o.Subsystem == "all" {
		return nil
	}
	return strings.Split(o.Subsystem, ",")
}

// RunInspect inspects cluster health over SSH.
func RunInspect(opts InspectOptions) error {
	// Load .env file from current directory (only sets unset vars)
	loadDotEnv(".env")

	if err := opts.Validate(); err != nil {
		return err
	}

	// Nodes come from the caller when it resolved them (the normal path), and
	// from an explicit --config file otherwise.
	nodes := opts.Nodes
	if len(nodes) == 0 {
		loaded, err := inspector.LoadNodes(opts.ConfigPath)
		if err != nil {
			return fmt.Errorf("loading %s: %w", opts.ConfigPath, err)
		}
		nodes = inspector.FilterByEnv(loaded, opts.Env)
	}
	if len(nodes) == 0 {
		return clierr.NotFound("no nodes found for environment %q", opts.Env)
	}

	// Prepare wallet-derived SSH keys
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return clierr.Failure("failed to prepare SSH keys: %w", err)
	}
	defer cleanup()

	return inspectNodes(nodes, opts, os.Stdout, os.Stderr)
}

// inspectNodes collects from nodes, checks, and reports.
//
// stdout carries the report and nothing else, so `--format json` is one JSON
// document a program can parse; everything that says what the command is doing
// goes to stderr.
func inspectNodes(nodes []inspector.Node, opts InspectOptions, stdout, stderr io.Writer) error {
	subsystems := opts.subsystems()

	fmt.Fprintf(stderr, "Inspecting %d %s nodes", len(nodes), opts.Env)
	if len(subsystems) > 0 {
		fmt.Fprintf(stderr, " [%s]", strings.Join(subsystems, ","))
	}
	if opts.AIEnabled {
		fmt.Fprintf(stderr, " (AI: %s)", opts.AIModel)
	}
	fmt.Fprintf(stderr, "...\n\n")

	// Phase 1: Collect
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout+10*time.Second)
	defer cancel()

	if opts.Verbose {
		fmt.Fprintf(stderr, "Collecting data from %d nodes (timeout: %s)...\n", len(nodes), opts.Timeout)
	}

	data := inspector.Collect(ctx, nodes, subsystems, opts.Verbose)

	if opts.Verbose {
		fmt.Fprintf(stderr, "Collection complete in %.1fs\n\n", data.Duration.Seconds())
	}

	// Phase 2: Check
	results := inspector.RunChecks(data, subsystems)

	// Phase 3: Report
	switch opts.Format {
	case "json":
		inspector.PrintJSON(results, stdout)
	default:
		inspector.PrintTable(results, stdout)
	}

	// Phase 4: AI Analysis (if enabled and there are failures or warnings)
	var analysis *inspector.AnalysisResult
	if opts.AIEnabled {
		analysis = analyzeInspection(results, data, opts, stdout, stderr)
	}

	// Phase 5: Write results to disk (if --output is set)
	if opts.OutputDir != "" {
		outPath, err := inspector.WriteResults(opts.OutputDir, opts.Env, results, data, analysis)
		if err != nil {
			return clierr.Failure("could not save the results to %s: %w", opts.OutputDir, err)
		}
		fmt.Fprintf(stderr, "\nResults saved to %s\n", outPath)
	}

	// A failed check is a failed command.
	if failures := results.Failures(); len(failures) > 0 {
		return fmt.Errorf("%d health check(s) failed", len(failures))
	}
	return nil
}

// analyzeInspection runs the AI analysis of what failed, and returns it, or nil
// when nothing needed analysing or the analysis failed. A failed analysis is
// reported on stderr and does not change the inspection's result: the checks
// are the result, the analysis is commentary on them.
func analyzeInspection(results *inspector.Results, data *inspector.ClusterData, opts InspectOptions, stdout, stderr io.Writer) *inspector.AnalysisResult {
	issues := results.FailuresAndWarnings()
	if len(issues) == 0 {
		fmt.Fprintf(stderr, "\nAll checks passed — no AI analysis needed.\n")
		return nil
	}

	var analysis *inspector.AnalysisResult
	var err error
	if opts.OutputDir != "" {
		// Per-group AI analysis for file output
		groups := inspector.GroupFailures(results)
		fmt.Fprintf(stderr, "\nAnalyzing %d unique issues with %s...\n", len(groups), opts.AIModel)
		analysis, err = inspector.AnalyzeGroups(groups, results, data, opts.AIModel, opts.AIAPIKey)
	} else {
		// Per-subsystem AI analysis for terminal output
		subs := map[string]bool{}
		for _, c := range issues {
			subs[c.Subsystem] = true
		}
		fmt.Fprintf(stderr, "\nAnalyzing %d issues across %d subsystems with %s...\n", len(issues), len(subs), opts.AIModel)
		analysis, err = inspector.Analyze(results, data, opts.AIModel, opts.AIAPIKey)
	}
	if err != nil {
		fmt.Fprintf(stderr, "\nAI analysis failed: %v\n", err)
		return nil
	}
	// The analysis is part of the report, but a JSON report is one document.
	if opts.Format == "json" {
		inspector.PrintAnalysis(analysis, stderr)
	} else {
		inspector.PrintAnalysis(analysis, stdout)
	}
	return analysis
}
