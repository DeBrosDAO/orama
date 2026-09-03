package rollout

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/build"
	"github.com/DeBrosOfficial/network/pkg/cli/production/push"
	"github.com/DeBrosOfficial/network/pkg/cli/production/upgrade"
	rolloutplan "github.com/DeBrosOfficial/network/pkg/rollout"
)

// Flags holds rollout command flags.
type Flags struct {
	Env     string // Target environment (devnet, testnet)
	NoBuild bool   // Skip the build step
	Yes     bool   // Skip confirmation
	Delay   int    // Seconds a node has to rejoin before the rollout stops
}

// Handle is the entry point for the rollout command.
func Handle(args []string) {
	flags, err := parseFlags(args)
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := execute(flags); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("rollout", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}
	fs.StringVar(&flags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	fs.BoolVar(&flags.NoBuild, "no-build", false, "Skip build step (use existing archive)")
	fs.BoolVar(&flags.Yes, "yes", false, "Skip confirmation")
	fs.IntVar(&flags.Delay, "delay", int(rolloutplan.GateBudget.Seconds()),
		"Seconds a node has to rejoin the cluster after its upgrade before the rollout stops")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if flags.Env == "" {
		return nil, fmt.Errorf("--env is required\nUsage: orama node rollout --env <devnet|testnet>")
	}

	return flags, nil
}

func execute(flags *Flags) error {
	start := time.Now()

	fmt.Printf("Rollout to %s\n", flags.Env)
	fmt.Printf("  Build:   %s\n", boolStr(!flags.NoBuild, "yes", "skip"))
	fmt.Printf("  Gate:    %ds for each node to rejoin\n\n", flags.Delay)

	// Step 1: Build
	if !flags.NoBuild {
		fmt.Printf("Step 1/3: Building binary archive...\n\n")
		buildFlags := &build.Flags{
			Arch: "amd64",
		}
		builder := build.NewBuilder(buildFlags)
		if err := builder.Build(); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
		fmt.Println()
	} else {
		fmt.Printf("Step 1/3: Build skipped (--no-build)\n\n")
	}

	// Step 2: Push
	fmt.Printf("Step 2/3: Pushing to all %s nodes...\n\n", flags.Env)
	push.Handle([]string{"--env", flags.Env})

	fmt.Println()

	// Step 3: Rolling upgrade
	fmt.Printf("Step 3/3: Rolling upgrade across %s...\n\n", flags.Env)
	upgradeArgs := []string{"--env", flags.Env, "--delay", fmt.Sprintf("%d", flags.Delay)}
	if flags.Yes {
		// Forward it, or the rolling upgrade prints its plan and stops — after
		// the build and push have already run.
		upgradeArgs = append(upgradeArgs, "--yes")
	}
	upgrade.Handle(upgradeArgs)

	elapsed := time.Since(start).Round(time.Second)
	fmt.Printf("\nRollout complete in %s\n", elapsed)
	return nil
}

func boolStr(b bool, trueStr, falseStr string) string {
	if b {
		return trueStr
	}
	return falseStr
}
