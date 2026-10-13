package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/nsledger"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// Bounds and reasons of the namespace cleanup.
const (
	// removeInterval and removeBudget bound the retries of one namespace's
	// removal: the gateway refuses a removal it can answer later.
	removeInterval = 10 * time.Second
	removeBudget   = 10 * time.Minute
	// removeTotal bounds one package's whole cleanup, however many namespaces.
	removeTotal = 30 * time.Minute
	// defaultNamespaceAge is the default --max-age of `sweep-namespaces`: over
	// the longest stage (chaos-soak, 180m) plus stages.StopGrace (10m). A
	// package whose runner is alive is protected by its run lock whatever its age.
	defaultNamespaceAge = 4 * time.Hour
	// testNamespacePrefix starts the name of every namespace a test creates.
	testNamespacePrefix = "e2e-"
	runnerRemoveReason  = "e2e: namespace left behind by a finished test package, removed by the runner"
	sweepRemoveReason   = "e2e: leftover test namespace, removed by e2e-fleet sweep-namespaces"
)

func removeOptions() nsledger.Options {
	return nsledger.Options{Interval: removeInterval, Budget: removeBudget, Total: removeTotal, Logf: stdLogger{}.Infof}
}

// removeLeftovers is the stage runner's AfterPackage: once a package's
// process is gone, whatever its exit, the namespaces it recorded in its
// ledger that still exist are removed as the run's operator. A budget cut,
// a kill or a cleanup the gateway refused leave namespaces behind; this is
// what keeps them from accumulating on the cluster.
func removeLeftovers(st *fleet.State) func(ctx context.Context, evidenceDir string) error {
	return func(ctx context.Context, evidenceDir string) error {
		remove := nsledger.OperatorRemover(oramacli.ForState(st, nil), runnerRemoveReason)
		return nsledger.Reconcile(ctx, []string{filepath.Join(evidenceDir, nsledger.FileName)}, time.Time{}, remove, removeOptions())
	}
}

// ledgerRoots are the artifact directories whose ledgers belong to st's
// cluster: the stagenet cluster is shared by every stagenet run, so all of
// them; a disposable fleet's own run only.
func ledgerRoots(st *fleet.State) ([]string, error) {
	if !st.IsStagenet() {
		return []string{st.ArtifactDir}, nil
	}
	roots, err := filepath.Glob(filepath.Join(filepath.Dir(st.ArtifactDir), "stagenet-*"))
	if err != nil {
		return nil, fmt.Errorf("failed to list the stagenet run directories: %w", err)
	}
	return roots, nil
}

// ledgerPaths are the namespace ledgers under the run directories roots: one
// per package of a run and one per package of its re-runs.
func ledgerPaths(roots []string) ([]string, error) {
	var out []string
	for _, root := range roots {
		for _, dir := range []string{filepath.Join(root, evidence.DirName), filepath.Join(root, stages.RerunDir, evidence.DirName)} {
			found, err := filepath.Glob(filepath.Join(dir, "*", nsledger.FileName))
			if err != nil {
				return nil, fmt.Errorf("failed to list the ledgers under %s: %w", dir, err)
			}
			out = append(out, found...)
		}
	}
	slices.Sort(out)
	return out, nil
}

// sweepInput is what sweepNamespaces needs.
type sweepInput struct {
	ledgers []string
	// cutoff: only ledger entries recorded at or before it are removed.
	cutoff time.Time
	remove nsledger.Remover
	// listed returns the e2e namespaces the operator's wallet owns on the
	// cluster; nil leaves them alone (--listed).
	listed func(ctx context.Context) ([]string, error)
	opt    nsledger.Options
}

// sweepNamespaces removes the test namespaces the ledgers still hold, and,
// when asked, the ones the operator owns that no ledger names: namespaces
// that older runs, before the ledger, left, whose age is unknown.
func sweepNamespaces(ctx context.Context, in sweepInput) error {
	errs := []error{nsledger.Reconcile(ctx, in.ledgers, in.cutoff, in.remove, in.opt)}
	if in.listed == nil {
		return errors.Join(errs...)
	}
	names, err := in.listed(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	return errors.Join(append(errs, nsledger.RemoveAll(ctx, names, in.remove, in.opt))...)
}

// listedTestNamespaces is `orama namespace list --json` of the run's
// operator, the names that start with testNamespacePrefix.
func listedTestNamespaces(cli *oramacli.Runner) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		res, err := cli.Run(ctx, "namespace", "list", "--json")
		if err != nil {
			return nil, err
		}
		if res.Exit != 0 {
			return nil, fmt.Errorf("orama namespace list exited %d: %s", res.Exit, strings.TrimSpace(res.Stderr))
		}
		var rows []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &rows); err != nil {
			return nil, fmt.Errorf("failed to parse `orama namespace list --json`: %w", err)
		}
		var names []string
		for _, r := range rows {
			if strings.HasPrefix(r.Name, testNamespacePrefix) {
				names = append(names, r.Name)
			}
		}
		return names, nil
	}
}

func cmdSweepNamespaces(parent context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("sweep-namespaces", flag.ContinueOnError)
	maxAge := fs.Duration("max-age", defaultNamespaceAge, "remove namespaces the ledgers name that are older than this")
	listed := fs.Bool("listed", false, "also remove every e2e-* namespace the operator owns that no ledger names (age unknown: run it when no test run is in progress)")
	if err := parseFlags(fs, args); err != nil {
		return exitUsage, err
	}
	if *maxAge <= 0 {
		return exitUsage, errors.Join(errUsage, errors.New("--max-age must be positive"))
	}
	ctx, stop := withSignals(parent)
	defer stop()
	st, _, err := loadState()
	if err != nil {
		return exitFail, err
	}
	if st.IsStagenet() {
		if err := signInStagenetOperator(ctx, st); err != nil {
			return exitFail, err
		}
	}
	roots, err := ledgerRoots(st)
	if err != nil {
		return exitFail, err
	}
	idle, held, err := idleRoots(roots)
	if err != nil {
		return exitFail, err
	}
	if len(held) > 0 && *listed {
		return exitFail, fmt.Errorf("--listed refused: a test run is in progress (%s), and its namespaces cannot be told from leftovers", strings.Join(held, ", "))
	}
	for _, h := range held {
		stdLogger{}.Infof("skipping %s: its runner is still running", h)
	}
	ledgers, err := ledgerPaths(idle)
	if err != nil {
		return exitFail, err
	}
	cli := oramacli.ForState(st, nil)
	in := sweepInput{ledgers: ledgers, cutoff: time.Now().Add(-*maxAge), remove: nsledger.OperatorRemover(cli, sweepRemoveReason), opt: removeOptions()}
	if *listed {
		in.listed = listedTestNamespaces(cli)
	}
	if err := sweepNamespaces(ctx, in); err != nil {
		return exitFail, fmt.Errorf("sweep-namespaces: %w", err)
	}
	return exitOK, nil
}
