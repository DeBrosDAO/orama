package stages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// BuildTag is the build tag every feature test file carries.
const BuildTag = "e2e_fleet"

// GoTestDir is where each package's go test -json output goes, in the artifact dir.
const GoTestDir = "gotest"

// StderrSuffix names the file beside a package's go test output that holds
// the go command's stderr.
const StderrSuffix = ".stderr"

// Runner executes stages.
type Runner struct {
	// ModuleDir is the e2e module root; packages are ./features/<id>.
	ModuleDir string
	// StatePath is the fleet state file handed to every package.
	StatePath string
	// ArtifactDir receives go test output and the stage state file.
	ArtifactDir string
	// BaseEnv is the environment packages start from (os.Environ plus the
	// resolved Go caches in production). Only what FeatureEnv allows of it
	// reaches a package: never a cloud credential.
	BaseEnv []string
	// ExtraEnv is added after the filter: KEY=VALUE pairs the runner itself
	// hands every package (E2E_BROKER_SOCK).
	ExtraEnv []string
	Exec     Executor
	Now      func() time.Time
	Logf     func(format string, args ...any)
	// AfterDestructive runs after every destructive package, even when the
	// run is being stopped (its context is not the run's): it restores what
	// the package may have left on the nodes (cmd/e2e-fleet sweeps the run's
	// tagged iptables rules and restores NTP). Its failure is the package's
	// runner error. nil does nothing.
	AfterDestructive func(ctx context.Context) error
	// Redactor returns the run's redactor, read afresh after each package
	// (feature processes register the credentials they mint in the run's
	// token registry). nil redacts the built-in patterns only.
	Redactor func() (*secrets.Redactor, error)
}

// Options select what Run executes.
type Options struct {
	// Only runs just this stage id (0: all).
	Only int
	// Resume skips stages the state file records as completed.
	Resume bool
}

// Run executes steps in order and returns the timeline. Test failures do not
// stop later stages: every stage's results belong in the report. A resumed
// run, and a run of one stage (Only), start from the timeline already in the
// artifact dir and replace only the stages they run: the other stages'
// results stay in the report.
func (r *Runner) Run(ctx context.Context, steps []Step, opt Options) (*Timeline, error) {
	statePath := filepath.Join(r.ArtifactDir, StateFileName)
	tl := &Timeline{}
	if opt.Resume || opt.Only != 0 {
		loaded, err := LoadTimeline(statePath)
		if err != nil {
			return nil, err
		}
		tl = loaded
	}
	if err := os.MkdirAll(filepath.Join(r.ArtifactDir, GoTestDir), 0o700); err != nil {
		return nil, fmt.Errorf("failed to create go test output dir: %w", err)
	}
	for _, step := range steps {
		if (opt.Only != 0 && step.Stage.ID != opt.Only) || (opt.Resume && tl.Completed(step.Stage.ID)) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return tl, fmt.Errorf("stopped before stage %d (%s): %w", step.Stage.ID, step.Stage.Name, err)
		}
		run := r.runStep(ctx, step)
		tl.put(run)
		if err := tl.Save(statePath); err != nil {
			return tl, err
		}
	}
	return tl, nil
}

func (r *Runner) runStep(ctx context.Context, step Step) StageRun {
	r.Logf("stage %d (%s): %d parallel, %d destructive", step.Stage.ID, step.Stage.Name, len(step.Parallel), len(step.Destructive))
	run := StageRun{Stage: step.Stage, Start: r.Now()}
	results := make([]PackageRun, len(step.Parallel))
	var wg sync.WaitGroup
	for i, feature := range step.Parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = r.runPackage(ctx, step.Stage, feature)
		}()
	}
	wg.Wait()
	run.Packages = results
	for _, feature := range step.Destructive {
		pr := r.runPackage(ctx, step.Stage, feature)
		if err := r.restoreAfter(ctx, feature); err != nil {
			pr.Error = strings.Join(slices.DeleteFunc([]string{pr.Error, err.Error()}, func(s string) bool { return s == "" }), "; ")
		}
		run.Packages = append(run.Packages, pr)
	}
	run.End = r.Now()
	run.Completed = ctx.Err() == nil
	return run
}

// restoreAfter runs AfterDestructive for feature on a context the run's
// cancellation does not reach.
func (r *Runner) restoreAfter(ctx context.Context, feature string) error {
	if r.AfterDestructive == nil {
		return nil
	}
	if err := r.AfterDestructive(context.WithoutCancel(ctx)); err != nil {
		r.Logf("restoring the nodes after %s failed: %v", feature, err)
		return fmt.Errorf("restoring the nodes after the destructive package failed: %w", err)
	}
	return nil
}

// runPackage runs one feature package once. It is never re-run here: a failure
// stays a failure (see Rerun for the separate flakiness label).
func (r *Runner) runPackage(ctx context.Context, stage Stage, feature string) PackageRun {
	name := fmt.Sprintf("stage-%02d-%s", stage.ID, feature)
	rel := filepath.Join(GoTestDir, name+".json")
	pr := PackageRun{Feature: feature, Output: rel, Evidence: filepath.Join(evidence.DirName, name), Start: r.Now()}
	args := []string{"go", "test", "-tags", BuildTag, "-json", "-count=1",
		"-timeout", time.Duration(stage.Timeout).String(), "./features/" + feature}
	pr.Exit, pr.Error = r.execTo(ctx, rel, pr.Evidence, args)
	pr.End = r.Now()
	r.Logf("stage %d: %s exit %d", stage.ID, feature, pr.Exit)
	return pr
}

// execTo runs args with stdout into the artifact file rel and stderr beside
// it, and the package's evidence in evDir (relative to the artifact dir),
// emptied first: a re-run of the package replaces its output and its
// evidence together, so records of two attempts never interleave. Both
// output files are redacted once the process is done.
func (r *Runner) execTo(ctx context.Context, rel, evDir string, args []string) (int, string) {
	evAbs := filepath.Join(r.ArtifactDir, evDir)
	if err := os.RemoveAll(evAbs); err != nil {
		return -1, fmt.Sprintf("failed to clear evidence dir %s: %v", evDir, err)
	}
	if err := os.MkdirAll(evAbs, 0o700); err != nil {
		return -1, fmt.Sprintf("failed to create evidence dir %s: %v", evDir, err)
	}
	out, err := os.OpenFile(filepath.Join(r.ArtifactDir, rel), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return -1, fmt.Sprintf("failed to create %s: %v", rel, err)
	}
	defer out.Close()
	errFile, err := os.OpenFile(filepath.Join(r.ArtifactDir, rel+StderrSuffix), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return -1, fmt.Sprintf("failed to create %s%s: %v", rel, StderrSuffix, err)
	}
	defer errFile.Close()
	env := append(append(FeatureEnv(r.BaseEnv), r.ExtraEnv...), config.EnvState+"="+r.StatePath, config.EnvStrict+"=1",
		config.EnvEvidenceDir+"="+evAbs)
	exit, err := r.Exec(ctx, Command{Dir: r.ModuleDir, Env: env, Args: args, Stdout: out, Stderr: errFile})
	msgs := []string{}
	if err != nil {
		msgs = append(msgs, err.Error())
	}
	if rerr := r.redactOutputs(rel); rerr != nil {
		msgs = append(msgs, rerr.Error())
	}
	return exit, strings.Join(msgs, "; ")
}

// redactOutputs applies the run's redactor to a package's output files.
// When the redactor cannot be loaded (an unreadable or oversized token
// registry) this package's output is withheld, never left unredacted; the
// next package loads the redactor afresh.
func (r *Runner) redactOutputs(rel string) error {
	red := secrets.NewRedactor()
	var loadErr error
	if r.Redactor != nil {
		red, loadErr = r.Redactor()
	}
	var errs []error
	for _, p := range []string{rel, rel + StderrSuffix} {
		path := filepath.Join(r.ArtifactDir, p)
		if loadErr != nil {
			errs = append(errs, withhold(path))
			continue
		}
		errs = append(errs, gotest.RedactFile(path, red.Redact))
	}
	if loadErr != nil {
		errs = append([]error{fmt.Errorf("failed to load the run's redactor for %s, its output was withheld: %w", rel, loadErr)}, errs...)
	}
	return errors.Join(errs...)
}

// withhold replaces the file at path with secrets.Withheld.
func withhold(path string) error {
	if err := os.WriteFile(path, []byte(secrets.Withheld+"\n"), 0o600); err != nil {
		return fmt.Errorf("failed to withhold %s: %w", path, err)
	}
	return nil
}
