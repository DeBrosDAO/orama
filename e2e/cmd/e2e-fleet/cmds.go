package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
	"github.com/DeBrosOfficial/network/e2e/harness/report"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// Sweep ages: the default for a resource without an e2e-ttl label, and the
// least --max-age accepted without --force.
const (
	defaultSweepAge = provision.DefaultTTL
	minSweepAge     = time.Hour
)

func nowUTC() time.Time { return time.Now().UTC() }

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	return nil
}

func cmdProvision(parent context.Context, args []string) (int, error) {
	if err := refuseStagenetEnv("provision"); err != nil {
		return exitFail, err
	}
	if err := parseFlags(flag.NewFlagSet("provision", flag.ContinueOnError), args); err != nil {
		return exitUsage, err
	}
	ctx, stop := withSignals(parent)
	defer stop()
	rs, err := prepare(ctx)
	if err != nil {
		return exitFail, err
	}
	return rs.provision(ctx, parent)
}

// provision brings the fleet up for later `test` runs. Anything short of a
// fleet that passes the guards — Up failing, an interrupt, a guard refusal —
// tears down what was created, by the run's label, but only when this run
// created something: a colliding run id is never torn down.
func (rs *runState) provision(ctx, parent context.Context) (int, error) {
	st, err := provisionUp(ctx, rs.cfg, stdLogger{})
	if err != nil {
		rs.owned = provision.OwnsResources(err)
	} else {
		rs.st = st
		if gerr := checkProvisioned(st, rs.realHome); gerr != nil {
			err = fmt.Errorf("post-provision guard, tearing down: %w", gerr)
		}
	}
	if err != nil && !rs.ownsFleet() {
		return exitFail, err
	}
	if err != nil {
		return exitFail, errors.Join(err, rs.teardown(parent, false))
	}
	fmt.Printf("E2E_FLEET_STATE=%s\n", rs.statePath())
	return exitOK, nil
}

func cmdTest(parent context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	only := fs.Int("stage", 0, "run only this stage id")
	resume := fs.Bool("resume", false, "skip stages already completed in this artifact dir")
	if err := parseFlags(fs, args); err != nil {
		return exitUsage, err
	}
	ctx, stop := withSignals(parent)
	defer stop()
	lay, err := findLayout()
	if err != nil {
		return exitFail, err
	}
	st, statePath, err := loadState()
	if err != nil {
		return exitFail, err
	}
	if !st.IsStagenet() {
		if _, err := runTTL(lay, os.LookupEnv); err != nil {
			return exitFail, err
		}
	}
	steps, err := planStages(lay)
	if err != nil {
		return exitFail, err
	}
	err = withBroker(ctx, lay, st, statePath, func(r *stages.Runner) error {
		_, err := r.Run(ctx, steps, stages.Options{Only: *only, Resume: *resume})
		return err
	})
	if err != nil {
		return exitFail, err
	}
	return writeReport(lay, reportRun{artifactDir: st.ArtifactDir, statePath: statePath, runID: st.RunID}, false)
}

func cmdTeardown(parent context.Context, args []string) (int, error) {
	if err := refuseStagenetEnv("teardown"); err != nil {
		return exitFail, err
	}
	if err := parseFlags(flag.NewFlagSet("teardown", flag.ContinueOnError), args); err != nil {
		return exitUsage, err
	}
	st, _, err := loadState()
	if err != nil {
		return exitFail, err
	}
	ctx, cancel := context.WithTimeout(parent, teardownBudget)
	defer cancel()
	if err := provisionDown(ctx, st, stdLogger{}); err != nil {
		return exitFail, fmt.Errorf("teardown of %s: %w", st.RunID, err)
	}
	return exitOK, nil
}

func cmdSweep(parent context.Context, args []string) (int, error) {
	if err := refuseStagenetEnv("sweep"); err != nil {
		return exitFail, err
	}
	fs := flag.NewFlagSet("sweep", flag.ContinueOnError)
	maxAge := fs.Duration("max-age", defaultSweepAge, "delete e2e resources without an e2e-ttl label older than this (labelled ones go at their own TTL)")
	force := fs.Bool("force", false, "accept a --max-age under "+minSweepAge.String())
	if err := parseFlags(fs, args); err != nil {
		return exitUsage, err
	}
	if err := checkSweepAge(*maxAge, *force); err != nil {
		return exitUsage, err
	}
	if missing := secrets.MissingEnv(config.RequiredRunEnv, secrets.LookupEnv); len(missing) > 0 {
		return exitFail, fmt.Errorf("missing required environment: %s", strings.Join(missing, ", "))
	}
	ctx, cancel := context.WithTimeout(parent, teardownBudget)
	defer cancel()
	deleted, err := provision.Sweep(ctx, *maxAge, stdLogger{})
	for _, d := range deleted {
		fmt.Println("deleted", d)
	}
	if err != nil {
		return exitFail, fmt.Errorf("sweep: %w", err)
	}
	return exitOK, nil
}

// checkSweepAge refuses a max age under minSweepAge unless forced: a short
// one would take the unlabelled resources of a run that is starting.
func checkSweepAge(maxAge time.Duration, force bool) error {
	if maxAge < minSweepAge && !force {
		return fmt.Errorf("%w: --max-age %s is under %s and could take a run that is starting; add --force if you mean it", errUsage, maxAge, minSweepAge)
	}
	return nil
}

func cmdReport(_ context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	bugs := fs.Bool("bug-drafts", false, "also write bugboard-drafts.json")
	if err := parseFlags(fs, args); err != nil {
		return exitUsage, err
	}
	lay, err := findLayout()
	if err != nil {
		return exitFail, err
	}
	st, statePath, err := loadState()
	if err != nil {
		return exitFail, err
	}
	return writeReport(lay, reportRun{artifactDir: st.ArtifactDir, statePath: statePath, runID: st.RunID}, *bugs)
}

func writeReport(lay layout, run reportRun, bugs bool) (int, error) {
	commit, err := commitOf(context.Background(), lay.repo)
	if err != nil {
		return exitFail, err
	}
	run.commit = commit
	rep, err := buildReport(lay, run, nil)
	if err != nil {
		return exitFail, err
	}
	if err := report.WriteAll(run.artifactDir, rep, bugs); err != nil {
		return exitFail, err
	}
	fmt.Println(rep.Notification)
	return exitCode(rep.Verdict), nil
}

func cmdCoverage(_ context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the whole matrix as JSON")
	if err := parseFlags(fs, args); err != nil {
		return exitUsage, err
	}
	enforce, err := coverageEnforced()
	if err != nil {
		return exitUsage, err
	}
	lay, err := findLayout()
	if err != nil {
		return exitFail, err
	}
	res, err := evaluateCoverage(lay)
	if err != nil {
		return exitFail, err
	}
	if *asJSON {
		if err := writeJSON(res); err != nil {
			return exitFail, err
		}
	} else {
		fmt.Print(res.Format())
		fmt.Println("coverage:", res.Summary())
	}
	return coverageExit(res.OK(), enforce), nil
}

// coverageExit fails only when the gate fails and is enforced.
func coverageExit(ok, enforce bool) int {
	switch {
	case ok:
		return exitOK
	case enforce:
		return exitFail
	}
	fmt.Fprintln(os.Stderr, "coverage gate FAILED but E2E_COVERAGE_ENFORCE=0: not blocking (see e2e/README.md, \"Coverage gate\")")
	return exitOK
}
