package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/artifacts"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
	"github.com/DeBrosOfficial/network/e2e/harness/report"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// Budgets of the run's own steps.
const (
	teardownBudget = 30 * time.Minute
	collectBudget  = 20 * time.Minute
	rerunBudget    = stages.Duration(15 * time.Minute)
)

// provisionUp, provisionDown and provisionLiveExtras are the provisioner;
// tests replace them.
var (
	provisionUp         = provision.Up
	provisionDown       = provision.Down
	provisionLiveExtras = provision.LiveExtras
)

// agentLogArtifact is the redacted copy of the test agent's log.
const agentLogArtifact = "agent.log"

// stopSignals end a run: Ctrl-C, a kill, and a closed terminal or SSH session.
var stopSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// withSignals cancels the returned context on any of stopSignals and keeps
// catching them afterwards, so a second signal cannot kill the teardown that
// the first one started.
func withSignals(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, stopSignals...)
	go func() {
		for sig := range ch {
			fmt.Fprintf(os.Stderr, "e2e-fleet: %s received: stopping the stages; teardown still runs\n", sig)
			cancel()
		}
	}()
	return ctx, func() { signal.Stop(ch); close(ch); cancel() }
}

// runState carries one `run` through its steps.
type runState struct {
	lay      layout
	cfg      provision.Config
	st       *fleet.State
	commit   string
	realHome string
	start    time.Time
	runErrs  []string
	bugs     bool
	// owned is set when a failed Up reports it registered resources under
	// the run's label: only then (or with a state) is there anything to
	// tear down that this run created.
	owned bool
}

func cmdRun(parent context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	keep := fs.Bool("keep-on-fail", false, "leave the fleet up when the verdict is not PASS (tear down later with `e2e-fleet teardown`)")
	bugs := fs.Bool("bug-drafts", false, "also write bugboard-drafts.json")
	if err := fs.Parse(args); err != nil {
		return exitUsage, errors.Join(errUsage, err)
	}
	ctx, stop := withSignals(parent)
	defer stop()
	rs, err := prepare(ctx)
	if err != nil {
		return exitFail, err
	}
	rs.bugs = *bugs
	return rs.run(ctx, parent, *keep)
}

// run provisions, tests, reports and tears down. Teardown is deferred before
// Up (Down finds everything by the run's label, so it also removes what a
// failed or interrupted Up left behind) but only runs when this run owns
// something: a state, or a failed Up that registered resources. A run id
// another fleet uses is never torn down. A failed teardown reaches both the
// exit code and the report.
func (rs *runState) run(ctx, parent context.Context, keepOnFail bool) (code int, err error) {
	code = exitFail
	defer func() {
		if !rs.ownsFleet() {
			return
		}
		keep := keepOnFail && code != exitOK && rs.st != nil && rs.guarded()
		if terr := rs.teardown(parent, keep); terr != nil {
			code, err = rs.teardownFailed(code, err, terr)
		}
	}()
	st, upErr := provisionUp(ctx, rs.cfg, stdLogger{})
	if upErr != nil {
		rs.owned = provision.OwnsResources(upErr)
		rs.runErrs = append(rs.runErrs, "provision: "+upErr.Error())
		return rs.finish()
	}
	rs.st = st
	if gerr := checkProvisioned(st, rs.realHome); gerr != nil {
		rs.runErrs = append(rs.runErrs, "post-provision guard: "+gerr.Error())
		return rs.finish()
	}
	rs.testAndCollect(ctx)
	return rs.finish()
}

// ownsFleet reports whether this run has anything of its own to tear down.
func (rs *runState) ownsFleet() bool { return rs.st != nil || rs.owned }

// guarded reports whether the provisioned state passes the guards: a fleet
// that fails them is never kept, whatever --keep-on-fail says.
func (rs *runState) guarded() bool {
	return checkProvisioned(rs.st, rs.realHome) == nil
}

// teardownFailed records a failed teardown as a run error, rewrites the
// report with it, and fails the run.
func (rs *runState) teardownFailed(code int, err, terr error) (int, error) {
	rs.runErrs = append(rs.runErrs, "teardown: "+terr.Error())
	if _, ferr := rs.finish(); ferr != nil {
		err = errors.Join(err, ferr)
	}
	return exitFail, errors.Join(err, fmt.Errorf("teardown failed, resources may be left (run `e2e-fleet sweep` to remove them by label): %w", terr))
}

// prepare runs the preflight and loads the provisioning config.
func prepare(ctx context.Context) (*runState, error) {
	lay, err := findLayout()
	if err != nil {
		return nil, err
	}
	realHome, err := secrets.RealHome()
	if err != nil {
		return nil, err
	}
	cfg, cfgErr := provision.LoadConfigFromEnv()
	if err := errors.Join(preflight(preflightInput{lookup: secrets.LookupEnv, realHome: realHome, runID: cfg.RunID}), cfgErr); err != nil {
		return nil, fmt.Errorf("preflight refused the run:\n%w", err)
	}
	if cfg.RepoRoot == "" {
		cfg.RepoRoot = lay.repo
	}
	if cfg.TTL, err = runTTL(lay, os.LookupEnv); err != nil {
		return nil, fmt.Errorf("preflight refused the run: %w", err)
	}
	commit, err := commitOf(ctx, lay.repo)
	if err != nil {
		return nil, err
	}
	stdLogger{}.Infof("run %s of %s; artifacts in %s", cfg.RunID, commit, cfg.ArtifactDir)
	return &runState{lay: lay, cfg: cfg, commit: commit, realHome: realHome, start: time.Now()}, nil
}

// statePath is where the run's state (and its token registry) lives.
func (rs *runState) statePath() string { return provision.StatePath(rs.cfg.WorkDir) }

// testAndCollect runs every stage, labels failures by a targeted re-run, and
// collects artifacts. Each step's failure is recorded; none stops the next.
func (rs *runState) testAndCollect(ctx context.Context) {
	steps, err := planStages(rs.lay)
	if err != nil {
		rs.runErrs = append(rs.runErrs, "stages: "+err.Error())
		return
	}
	err = withBroker(ctx, rs.lay, rs.st, rs.statePath(), func(r *stages.Runner) error {
		rs.runStages(ctx, r, steps)
		return nil
	})
	if err != nil {
		rs.runErrs = append(rs.runErrs, "stages: "+err.Error())
	}
	if err := rs.collect(ctx); err != nil {
		rs.runErrs = append(rs.runErrs, "artifacts: "+err.Error())
	}
}

// runStages runs every stage, then labels the failures by a targeted
// re-run, recording each step's failure. A stopped run labels nothing: the
// re-runs would only be cancelled.
func (rs *runState) runStages(ctx context.Context, r *stages.Runner, steps []stages.Step) {
	if _, err := r.Run(ctx, steps, stages.Options{}); err != nil {
		rs.runErrs = append(rs.runErrs, "stages: "+err.Error())
	}
	if ctx.Err() != nil {
		return
	}
	if err := rerunFailures(ctx, r, rs.lay); err != nil {
		rs.runErrs = append(rs.runErrs, "re-run: "+err.Error())
	}
}

// collect gathers artifacts, redacted with the run's secrets and every
// credential the feature processes minted: from every member of the state
// and every extra the broker created that is still up, plus a redacted copy
// of the test agent's log (which stays in the private work dir).
func (rs *runState) collect(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), collectBudget)
	defer cancel()
	red, err := secrets.ForRun(secrets.LookupEnv, rs.statePath())
	if err != nil {
		return err
	}
	st := *rs.st
	extras, lerr := provisionLiveExtras(cctx, rs.st)
	st.Extras = append(append([]fleet.Node{}, rs.st.Extras...), extras...)
	dir := filepath.Join(rs.st.ArtifactDir, artifacts.DirName)
	c := &artifacts.FleetCollector{Fleet: fleet.New(&st, nil), CLI: oramacli.ForState(rs.st, nil), Since: rs.start, Redactor: red}
	_, err = c.Collect(cctx, dir)
	return errors.Join(err, lerr, copyRedacted(provision.AgentLogPath(rs.cfg.WorkDir), filepath.Join(dir, agentLogArtifact), red.Redact))
}

// finish writes the report and returns the exit code of its verdict.
func (rs *runState) finish() (int, error) {
	dir := rs.cfg.ArtifactDir
	if rs.st != nil {
		dir = rs.st.ArtifactDir
	}
	rep, err := buildReport(rs.lay, reportRun{artifactDir: dir, statePath: rs.statePath(), runID: rs.cfg.RunID, commit: rs.commit}, rs.runErrs)
	if err != nil {
		return exitFail, err
	}
	if err := report.WriteAll(dir, rep, rs.bugs); err != nil {
		return exitFail, err
	}
	fmt.Println(rep.Notification)
	fmt.Println("report:", filepath.Join(dir, report.FileHTML))
	return exitCode(rep.Verdict), nil
}

// teardown runs on a context the signal does not cancel. It returns the
// teardown's failure; keeping the fleet is not one.
func (rs *runState) teardown(parent context.Context, keep bool) error {
	if keep {
		fmt.Printf("e2e-fleet: --keep-on-fail: the fleet is still up. Tear it down with:\n  %s=%s go run ./cmd/e2e-fleet teardown\n",
			"E2E_FLEET_STATE", rs.statePath())
		return nil
	}
	st := rs.st
	if st == nil {
		st = &fleet.State{RunID: rs.cfg.RunID}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), teardownBudget)
	defer cancel()
	if err := provisionDown(ctx, st, stdLogger{}); err != nil {
		fmt.Fprintf(os.Stderr, "e2e-fleet: TEARDOWN FAILED, resources may be left: %v\nrun `e2e-fleet sweep` to remove them by label\n", err)
		return err
	}
	return nil
}

func exitCode(verdict string) int {
	switch verdict {
	case report.VerdictPass:
		return exitOK
	case report.VerdictIncomplete:
		return exitIncomplete
	}
	return exitFail
}

func verdictOf(code int) string {
	switch code {
	case exitOK:
		return report.VerdictPass
	case exitIncomplete:
		return report.VerdictIncomplete
	}
	return report.VerdictFail
}
