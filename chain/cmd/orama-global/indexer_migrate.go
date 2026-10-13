package main

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/chain/indexer"
)

func indexerMigrateCmd() *cobra.Command {
	var home string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move an index of an older layout aside so the indexer starts a new one",
		Long: `migrate looks at the index in <home>/index. An index written in an older layout
than this indexer's cannot be brought up to date (the aggregates are computed as
blocks are ingested), and the indexer refuses to open it. migrate renames it to
<home>/index.v<layout version>-<UTC time> and leaves nothing at <home>/index, so
the indexer's next start begins a new index from --start-height. Nothing is
deleted: the old index stays on disk until you remove it. An index of the current
layout, or none, is left as it is; one of a newer layout is an error.

The indexer must not be running: the index is opened read-only, and a running
indexer holds it. 'orama upgrade' stops the indexer, runs this as the indexer's
own account (runuser -u orama-indexer), and starts it. Run it as that account,
not as root: the account owns <home>, and migrate refuses an index path that is a
symbolic link or not a directory.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runIndexerMigrate(cmd.OutOrStdout(), home, time.Now())
		},
	}
	cmd.Flags().StringVar(&home, "home", ".", "Indexer state directory")
	return cmd
}

// runIndexerMigrate moves the index under home aside if its layout is old, and says what it did.
func runIndexerMigrate(out io.Writer, home string, now time.Time) error {
	dir := filepath.Join(home, indexerDBDir)
	res, err := indexer.MoveAsideStale(dir, now)
	if err != nil {
		return err
	}
	switch {
	case !res.Present:
		fmt.Fprintf(out, "  indexer: no index in %s; the indexer starts a new one\n", dir)
	case res.MovedTo != "":
		slog.Warn("moved an index of an older layout aside", "from", dir, "to", res.MovedTo, "layout", res.Version, "current", res.Current)
		fmt.Fprintf(out, "  indexer: the index in %s has layout version %d and this indexer writes %d; moved it aside to %s (nothing deleted: remove it when you no longer need it) and the indexer starts a new one\n",
			dir, res.Version, res.Current, res.MovedTo)
	default:
		fmt.Fprintf(out, "  indexer: the index in %s has the current layout version %d; kept\n", dir, res.Current)
	}
	return nil
}
