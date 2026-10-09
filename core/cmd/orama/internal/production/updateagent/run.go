// Package updateagent wires the auto-update agent (pkg/autoupdate) to the node
// it runs on: its index RQLite, its installed release, `orama node
// stage-archive` and `orama node upgrade`. `orama node autoupdate run`, fired
// by orama-autoupdate.timer, is its only caller.
package updateagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

const (
	// workDir is where releases are fetched to and the install intent is kept:
	// root's alone, and on /var, not beside the install, since an archive is
	// hundreds of megabytes.
	workDir = "/var/lib/orama-autoupdate"
	// workDirPerm: only root reads what the agent keeps.
	workDirPerm = 0o700
	// rootUID owns workDir.
	rootUID = 0
	// dbPingBudget bounds the first contact with the index RQLite.
	dbPingBudget = 10 * time.Second
	// readStrong makes every read go through the leader: the install record and
	// the lock must not be read from a follower that has not caught up.
	readStrong rqlite.ReadConsistency = "strong"
)

// Run looks for a newer release on this cluster's channel and acts on it
// according to the cluster's policy, then prints what it did. A signal stops it
// the way it stops any other command, and an install it interrupted is
// finished by the next run.
func Run(ctx context.Context, out io.Writer) error {
	if err := clierr.RequireRoot("the auto-update agent"); err != nil {
		return err
	}
	if err := checkWorkDir(workDir, rootUID); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	unlock, err := lockRun(workDir)
	if errors.Is(err, errRunning) {
		fmt.Fprintf(out, "%s: %v\n", autoupdate.ActionNone, err)
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	agent, err := newAgent(out)
	if err != nil {
		return err
	}
	outcome, err := agent.Run(ctx)
	if outcome.Action != "" {
		fmt.Fprintf(out, "%s: %s\n", outcome.Action, printable(outcome.Reason))
	}
	return err
}

// newAgent is the agent for this node.
func newAgent(out io.Writer) (*autoupdate.Agent, error) {
	ep, err := rqlite.LocalNodeEndpoint()
	if err != nil {
		return nil, err
	}
	role, err := nodeRole()
	if err != nil {
		return nil, err
	}
	return &autoupdate.Agent{
		Store:   autoupdate.SQLStore{Open: indexOpener(ep)},
		Raft:    raftView{admin: ep.Admin()},
		Journal: fileJournal{path: filepath.Join(workDir, journalName)},
		Retries: fileRetries{path: filepath.Join(workDir, retryName)},
		Source: autoupdate.Source{
			RootPath: releaseverify.RootPath, SeenPath: releaseverify.SeenPath,
			WorkDir: workDir, Arch: runtime.GOARCH, Now: time.Now,
		},
		Node: newMachine(ep), Role: role, NodeHost: ep.Host, NoticePath: updatenotice.Path, Now: time.Now,
		Logf: func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) },
	}, nil
}

// indexOpener opens this node's index RQLite, checking that it answers. It is
// called for every store operation: an install restarts this node's RQLite, and
// the handle from before is not the one to record the result through.
func indexOpener(ep rqlite.Endpoint) autoupdate.Opener {
	return func(ctx context.Context) (*sql.DB, func(), error) {
		db, err := sql.Open("rqlite", ep.SQLDSN(readStrong))
		if err != nil {
			return nil, nil, fmt.Errorf("open the index rqlite at %s: %w", ep.BaseURL(), err)
		}
		pingCtx, cancel := context.WithTimeout(ctx, dbPingBudget)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			_ = db.Close()
			return nil, nil, fmt.Errorf("the index rqlite at %s does not answer (is orama-node running? try 'orama node status'): %w",
				ep.BaseURL(), err)
		}
		return db, func() { _ = db.Close() }, nil
	}
}

// printable drops the characters a terminal would act on from text that came
// from a repository.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
}
