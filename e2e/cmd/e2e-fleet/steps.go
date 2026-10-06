package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/coverage"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
	"github.com/DeBrosOfficial/network/e2e/harness/report"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

func newStageRunner(lay layout, statePath, artifactDir string) *stages.Runner {
	return &stages.Runner{
		ModuleDir: lay.module, StatePath: statePath, ArtifactDir: artifactDir,
		Exec: stages.ExecCommand, Now: nowUTC, Logf: stdLogger{}.Infof,
		Redactor: func() (*secrets.Redactor, error) { return secrets.ForRun(secrets.LookupEnv, statePath) },
	}
}

// restoreBudget bounds the node restoration after a destructive package.
const restoreBudget = 5 * time.Minute

// restoreNodes is the stage runner's AfterDestructive: the run's tagged
// iptables rules swept and NTP restored on every member of st's fleet.
func restoreNodes(st *fleet.State) func(context.Context) error {
	return func(ctx context.Context) error {
		rctx, cancel := context.WithTimeout(ctx, restoreBudget)
		defer cancel()
		return fleet.New(st, nil).RestoreNodes(rctx)
	}
}

func planStages(lay layout) ([]stages.Step, error) {
	st, err := stages.Load(filepath.Join(lay.module, stages.FilePath))
	if err != nil {
		return nil, err
	}
	ms, err := manifest.LoadAll(filepath.Join(lay.module, featuresDir))
	if err != nil {
		return nil, err
	}
	return stages.Plan(st, ms)
}

// rerunFailures re-runs each failed test once, alone, to label it. The results
// go to the rerun dir and never into the verdict.
func rerunFailures(ctx context.Context, r *stages.Runner, lay layout) error {
	in, err := report.LoadRun(r.ArtifactDir, filepath.Join(lay.module, featuresDir))
	if err != nil {
		return err
	}
	var failed []stages.FailedTest
	for _, f := range gotest.Failed(in.Results) {
		failed = append(failed, stages.FailedTest{Feature: filepath.Base(f.Package), Test: f.Test})
	}
	if len(failed) == 0 {
		return nil
	}
	_, err = r.Rerun(ctx, failed, rerunBudget)
	return err
}

// evaluateCoverage runs the gate over the repository.
func evaluateCoverage(lay layout) (coverage.Result, error) {
	universe, err := coverage.Universe(lay.repo)
	if err != nil {
		return coverage.Result{}, err
	}
	ms, err := manifest.LoadAll(filepath.Join(lay.module, featuresDir))
	if err != nil {
		return coverage.Result{}, err
	}
	waivers, err := coverage.LoadWaivers(filepath.Join(lay.module, waiversFile))
	if err != nil {
		return coverage.Result{}, err
	}
	return coverage.Evaluate(universe, ms, waivers), nil
}

// reportRun names the run a report is built for.
type reportRun struct {
	artifactDir string
	// statePath locates the run's token registry, beside the state.
	statePath string
	runID     string
	commit    string
}

// buildReport assembles the report from the artifact dir, redacted with the
// run's secrets and every credential its feature processes minted.
func buildReport(lay layout, run reportRun, runErrs []string) (report.Report, error) {
	if err := os.MkdirAll(run.artifactDir, 0o700); err != nil {
		return report.Report{}, fmt.Errorf("failed to create artifact dir %s: %w", run.artifactDir, err)
	}
	in, err := report.LoadRun(run.artifactDir, filepath.Join(lay.module, featuresDir))
	if err != nil {
		return report.Report{}, err
	}
	if in.Redactor, err = secrets.ForRun(secrets.LookupEnv, run.statePath); err != nil {
		return report.Report{}, err
	}
	cov, err := evaluateCoverage(lay)
	if err != nil {
		runErrs = append(runErrs, "coverage: "+err.Error())
	} else {
		in.Coverage = &cov
	}
	in.RunID, in.Commit, in.RunErrors = run.runID, run.commit, runErrs
	return report.Build(in), nil
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("failed to write JSON: %w", err)
	}
	return nil
}

// coverageEnforced reads E2E_COVERAGE_ENFORCE (default on).
func coverageEnforced() (bool, error) {
	return config.Bool(os.LookupEnv, config.EnvCoverageEnforce, true)
}
