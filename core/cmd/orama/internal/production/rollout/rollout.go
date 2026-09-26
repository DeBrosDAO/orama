package rollout

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/build"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/upgrade"
)

// Flags holds rollout command flags.
type Flags struct {
	Env     string // Target environment (devnet, testnet)
	NoBuild bool   // Skip the build step
	Archive string // With NoBuild: the archive to roll out
	Yes     bool   // Skip confirmation
	Delay   int    // Seconds a node has to rejoin before the rollout stops
}

// Run is the entry point for the rollout command.
func Run(flags *Flags) error {
	if err := flags.validate(); err != nil {
		return err
	}
	return execute(flags)
}

func (f *Flags) validate() error {
	if f.Env == "" {
		return fmt.Errorf("--env is required\nUsage: orama node rollout --env <devnet|testnet>")
	}
	if f.NoBuild && f.Archive == "" {
		return fmt.Errorf("--no-build needs --archive <path>: the build to roll out")
	}
	if !f.NoBuild && f.Archive != "" {
		return fmt.Errorf("--archive is only for --no-build; a rollout that builds rolls out what it built")
	}
	return nil
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
		flags.Archive = builder.OutputPath()
		fmt.Println()
	} else {
		fmt.Printf("Step 1/3: Build skipped (--no-build)\n\n")
	}

	// Step 2: Push
	fmt.Printf("Step 2/3: Pushing to all %s nodes...\n\n", flags.Env)
	if err := push.Run(&push.Flags{Env: flags.Env, Archive: flags.Archive}); err != nil {
		return fmt.Errorf("push failed: %w", err)
	}

	fmt.Println()

	// Step 3: Rolling upgrade.
	//
	// Yes is forwarded, or the rolling upgrade prints its plan and stops —
	// after the build and push have already run.
	fmt.Printf("Step 3/3: Rolling upgrade across %s...\n\n", flags.Env)
	if err := upgrade.Run(&upgrade.Flags{
		Env:   flags.Env,
		Delay: flags.Delay,
		Yes:   flags.Yes,
	}); err != nil {
		return fmt.Errorf("rolling upgrade failed: %w", err)
	}

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
