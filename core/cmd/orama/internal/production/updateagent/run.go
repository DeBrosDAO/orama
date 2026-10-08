// Package updateagent wires the auto-update agent (pkg/autoupdate) to the node
// it runs on: its index RQLite, its installed release, `orama node
// stage-archive` and `orama node upgrade`. `orama node autoupdate run`, fired
// by orama-autoupdate.timer, is its only caller.
package updateagent

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

const (
	// workDir is where releases are fetched to: root's alone, and on /var, not
	// beside the install, since an archive is hundreds of megabytes.
	workDir = "/var/lib/orama-autoupdate"
	// dbPingBudget bounds the first contact with the index RQLite.
	dbPingBudget = 10 * time.Second
	// readStrong makes every read go through the leader: the install record and
	// the lock must not be read from a follower that has not caught up.
	readStrong rqlite.ReadConsistency = "strong"
)

// Run looks for a newer release on this cluster's channel and acts on it
// according to the cluster's policy, then prints what it did.
func Run(ctx context.Context, out io.Writer) error {
	if err := clierr.RequireRoot("the auto-update agent"); err != nil {
		return err
	}
	ep, err := rqlite.LocalNodeEndpoint()
	if err != nil {
		return err
	}
	db, err := openIndex(ctx, ep)
	if err != nil {
		return err
	}
	defer db.Close()
	role, err := nodeRole()
	if err != nil {
		return err
	}
	machine, err := newMachine(ep)
	if err != nil {
		return err
	}
	agent := &autoupdate.Agent{
		Store: autoupdate.SQLStore{DB: db},
		Raft:  raftView{admin: ep.Admin()},
		Source: autoupdate.Source{
			RootPath: releaseverify.RootPath, SeenPath: releaseverify.SeenPath,
			WorkDir: workDir, Arch: runtime.GOARCH, Now: time.Now,
		},
		Node: machine, Role: role, NodeHost: ep.Host, NoticePath: updatenotice.Path, Now: time.Now,
		Logf: func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) },
	}
	outcome, err := agent.Run(ctx)
	if outcome.Action != "" {
		fmt.Fprintf(out, "%s: %s\n", outcome.Action, outcome.Reason)
	}
	return err
}

// openIndex opens this node's index RQLite and checks that it answers.
func openIndex(ctx context.Context, ep rqlite.Endpoint) (*sql.DB, error) {
	db, err := sql.Open("rqlite", ep.SQLDSN(readStrong))
	if err != nil {
		return nil, fmt.Errorf("open the index rqlite at %s: %w", ep.BaseURL(), err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, dbPingBudget)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("the index rqlite at %s does not answer (is orama-node running? try 'orama node status'): %w",
			ep.BaseURL(), err)
	}
	return db, nil
}
