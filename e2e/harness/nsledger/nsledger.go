// Package nsledger is the ledger of the namespaces a package run names, and
// the reconciliation that removes the ones still on the cluster.
//
// A test deletes its own namespaces in t.Cleanup, once: a cleanup that meets
// a transient refusal, a package that is killed, and a runner that dies all
// leave a namespace nobody deletes, holding port blocks and processes on
// three nodes. So the namespace's name is recorded before the namespace is
// created, in a file of the package's evidence directory (the runner owns
// that directory), and the runner removes every recorded namespace that is
// still there once the package exits, whatever the exit was. `e2e-fleet
// sweep-namespaces` does the same for ledgers of runs that never got that far.
package nsledger

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// FileName is the ledger's name inside an evidence directory. It does not
// end in .jsonl, the suffix of the evidence records the report loads.
const FileName = "namespaces.ledger"

// Prefix starts the name of every namespace the harness generates
// (ns.UniqueName). Only such names are recorded and only such names are ever
// removed: a ledger is a file, and a line in it must never be able to name
// a real namespace.
const Prefix = "e2e-"

// Entry is one namespace a package named.
type Entry struct {
	Namespace string
	At        time.Time
}

// line is one JSON line of the ledger: a namespace named, or (Gone) removed.
type line struct {
	Namespace string    `json:"namespace"`
	At        time.Time `json:"at"`
	Gone      bool      `json:"gone,omitempty"`
}

func appendLine(path string, l line) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("failed to encode a ledger line for %s: %w", l.Namespace, err)
	}
	// One write of one line: packages of a run never share a ledger, but a
	// sweep may mark an entry gone while its package's runner reads it.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open the namespace ledger %s: %w", path, err)
	}
	_, werr := f.Write(append(raw, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("failed to write the namespace ledger %s: %w", path, werr)
	}
	return nil
}

// Record names namespace in the ledger of dir, at at. A name without Prefix
// is refused.
func Record(dir, namespace string, at time.Time) error {
	if !strings.HasPrefix(namespace, Prefix) {
		return fmt.Errorf("refusing to record namespace %q: only %q names are tracked", namespace, Prefix)
	}
	return appendLine(filepath.Join(dir, FileName), line{Namespace: namespace, At: at.UTC()})
}

// RecordFromEnv records namespace in the ledger of the evidence directory
// lookup names. Outside a runner-driven package (no evidence directory) it
// does nothing: there is no runner to remove anything.
func RecordFromEnv(lookup func(string) (string, bool), namespace string, at time.Time) error {
	dir, ok := lookup(config.EnvEvidenceDir)
	if !ok || dir == "" {
		return nil
	}
	return Record(dir, namespace, at)
}

// MarkGone records that namespace no longer exists.
func MarkGone(path, namespace string) error {
	return appendLine(path, line{Namespace: namespace, Gone: true})
}

// Pending reads the ledger at path: the namespaces named and not marked
// gone, oldest first. A missing ledger is empty. A line that is not valid (a
// write torn by a kill) does not hide the others: the entries that read are
// returned together with an error naming each bad line.
func Pending(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open the namespace ledger %s: %w", path, err)
	}
	defer f.Close()
	var order []string
	listed := map[string]bool{}
	at := map[string]time.Time{}
	var bad []error
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil || l.Namespace == "" {
			bad = append(bad, fmt.Errorf("%s line %d is not a ledger line (skipped): %v", path, n, err))
			continue
		}
		if !listed[l.Namespace] && !l.Gone {
			listed[l.Namespace] = true
			order = append(order, l.Namespace)
		}
		if l.Gone {
			delete(at, l.Namespace)
			continue
		}
		at[l.Namespace] = l.At
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("failed to read the namespace ledger %s: %w", path, err)
	}
	var out []Entry
	for _, name := range order {
		if t, ok := at[name]; ok {
			out = append(out, Entry{Namespace: name, At: t})
		}
	}
	return out, errors.Join(bad...)
}

// ErrNotFound is what a Remover returns for a namespace that does not exist.
var ErrNotFound = errors.New("namespace not found")

// ErrPermanent marks a removal failure that waiting cannot fix (the operator
// is not authorized, the request is malformed): it is not retried.
var ErrPermanent = errors.New("permanent failure")

// ErrNotEphemeral is returned for a name without Prefix: it is never passed to
// a Remover.
var ErrNotEphemeral = errors.New("not a test namespace")

// Remover removes namespace from the cluster. nil and ErrNotFound both mean
// it is gone; any other error is retried until the budget is spent.
type Remover func(ctx context.Context, namespace string) error

// Options bound the retries of Reconcile.
type Options struct {
	// Interval is how long to wait between attempts on one namespace.
	Interval time.Duration
	// Budget is how long one namespace may take.
	Budget time.Duration
	// Total, when positive, bounds the whole Reconcile or RemoveAll call.
	Total time.Duration
	// Logf, when set, receives one line per namespace removed.
	Logf func(format string, args ...any)
}

// Reconcile removes every namespace the ledgers at paths still hold and
// marks it gone. Only entries recorded at or before cutoff are touched; a
// zero cutoff takes all of them. It goes on after a namespace it cannot
// remove and returns every failure: those entries stay in the ledger.
func Reconcile(ctx context.Context, paths []string, cutoff time.Time, remove Remover, opt Options) error {
	ctx, cancel := withTotal(ctx, opt)
	defer cancel()
	var errs []error
	for _, path := range paths {
		entries, err := Pending(path)
		if err != nil {
			errs = append(errs, err)
		}
		for _, e := range entries {
			if !cutoff.IsZero() && e.At.After(cutoff) {
				continue
			}
			if err := removeOne(ctx, path, e.Namespace, remove, opt); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func withTotal(ctx context.Context, opt Options) (context.Context, context.CancelFunc) {
	if opt.Total > 0 {
		return context.WithTimeout(ctx, opt.Total)
	}
	return ctx, func() {}
}

func removeOne(ctx context.Context, path, namespace string, remove Remover, opt Options) error {
	if err := RemoveAll(ctx, []string{namespace}, remove, opt); err != nil {
		return err
	}
	return MarkGone(path, namespace)
}

// RemoveAll removes each namespace, retrying a refused removal until
// opt.Budget, and goes on after one it cannot remove. It returns every
// failure.
func RemoveAll(ctx context.Context, namespaces []string, remove Remover, opt Options) error {
	ctx, cancel := withTotal(ctx, opt)
	defer cancel()
	var errs []error
	for _, namespace := range namespaces {
		if !strings.HasPrefix(namespace, Prefix) {
			errs = append(errs, fmt.Errorf("refusing to remove namespace %q: %w (only %q names are removed)", namespace, ErrNotEphemeral, Prefix))
			continue
		}
		removed := false
		err := eventually.Poll(ctx, opt.Interval, opt.Budget, "namespace "+namespace+" to be removed", func() (bool, error) {
			err := remove(ctx, namespace)
			if err == nil {
				removed = true
				return true, nil
			}
			if errors.Is(err, ErrNotFound) {
				return true, nil
			}
			if errors.Is(err, ErrPermanent) {
				return false, eventually.Stop(err)
			}
			return false, err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("namespace %s may be leaked: %w", namespace, err))
			continue
		}
		if removed && opt.Logf != nil {
			opt.Logf("removed leftover namespace %s", namespace)
		}
	}
	return errors.Join(errs...)
}
