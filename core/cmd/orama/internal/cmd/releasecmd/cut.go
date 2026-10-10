package releasecmd

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releasepub"
)

func newCutCmd(d deps) *cobra.Command {
	var f struct {
		dir, channel string
		archives     []string
		retention    int
		replace      bool
		dryRun       bool
	}
	cmd := &cobra.Command{
		Use:   "cut --channel nightly|main|dev/<branch> --archive <path>",
		Short: "List an archive on a channel and sign the metadata (3 approvals)",
		Long: `List one or more archives of one version (amd64 and arm64) under a channel and
sign the result: targets.json, then snapshot.json, then timestamp.json, three
approvals in the RootWallet desktop app. Each is announced first. The command
checks that a client would accept the metadata before it writes anything, and
writes only to --dir; it uploads nothing. Run publish to upload.

A version must be dotted numeric (0.3.1) and newer than the channel's newest;
a release is immutable (--replace is for a dev build that reuses a version).
Only the newest --retention versions of the channel stay listed. The timestamp
is valid 7 days for nightly and dev, 30 for main.

--dry-run plans the release and stops: nothing is signed, written or uploaded,
and the RootWallet is not contacted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			channel, err := releasepub.ParseChannel(f.channel)
			if err != nil {
				return clierr.Usage("--channel: %v", err)
			}
			if len(f.archives) == 0 {
				return clierr.Usage("--archive is required (repeat it for each architecture)")
			}
			repo, err := repoFor(f.dir)
			if err != nil {
				return err
			}
			p := releasepub.CutParams{
				Repo: repo, Channel: channel, Archives: f.archives, Retention: f.retention, Replace: f.replace,
				DryRun: f.dryRun, Now: d.now(), Progress: cmd.OutOrStdout(),
			}
			if !f.dryRun {
				p.Agent = d.agent()
			}
			plan, err := releasepub.Cut(cmd.Context(), p)
			if err != nil {
				return fail("cut the release", err)
			}
			printPlan(cmd, plan, f.dryRun, repo.Dir)
			return nil
		},
	}
	repoFlag(cmd, &f.dir)
	fl := cmd.Flags()
	fl.StringVar(&f.channel, "channel", "", "nightly, main or dev/<branch> [required]")
	fl.StringArrayVar(&f.archives, "archive", nil, "An orama-<version>-linux-<arch>.tar.gz to release; repeatable [required]")
	fl.IntVar(&f.retention, "retention", releasepub.DefaultRetention, "How many versions of the channel stay listed")
	fl.BoolVar(&f.replace, "replace", false, "Let a listed path change its bytes (dev builds only)")
	fl.BoolVar(&f.dryRun, "dry-run", false, "Plan the release; sign, write and upload nothing")
	_ = cmd.MarkFlagRequired("channel")
	return cmd
}

func printPlan(cmd *cobra.Command, plan *releasepub.CutPlan, dryRun bool, dir string) {
	w := cmd.OutOrStdout()
	verb := "Cut"
	if dryRun {
		verb = "Would cut"
	}
	printf(w, "%s %s %s: targets v%d, snapshot v%d, timestamp v%d (valid until %s)\n", verb, plan.Channel, plan.Version,
		plan.TargetsVersion, plan.SnapshotVersion, plan.TimestampVersion, plan.TimestampExpires.Format(time.RFC3339))
	for _, group := range []struct {
		label string
		paths []string
	}{{"list", plan.Added}, {"replace", plan.Replaced}, {"drop", plan.Dropped}} {
		for _, p := range group.paths {
			printf(w, "  %-7s %s\n", group.label, p)
		}
	}
	printf(w, "  GitHub release %s\n", plan.Tag)
	if dryRun {
		printf(w, "Nothing was signed or written. Run it without --dry-run to sign (3 approvals).\n")
		return
	}
	printf(w, "Written to %s. Upload with: orama maint release publish --dir %s\n", dir, dir)
}

func newRefreshCmd(d deps) *cobra.Command {
	var dir, channel string
	cmd := &cobra.Command{
		Use:   "refresh-timestamp --channel nightly|main|dev/<branch>",
		Short: "Re-sign only the timestamp (1 approval)",
		Long: `Re-sign the timestamp alone: the next version, naming the snapshot already in
--dir, valid 7 days (nightly, dev) or 30 (main) from now. Clients refuse a
timestamp that has expired, so a repository nobody cuts into needs this before
the last one runs out. Then publish.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ch, err := releasepub.ParseChannel(channel)
			if err != nil {
				return clierr.Usage("--channel: %v", err)
			}
			repo, err := repoFor(dir)
			if err != nil {
				return err
			}
			version, err := releasepub.RefreshTimestamp(cmd.Context(), d.agent(), repo, ch, d.now(), cmd.OutOrStdout())
			if err != nil {
				return fail("refresh the timestamp", err)
			}
			printf(cmd.OutOrStdout(), "timestamp version %d written to %s; publish it with: orama maint release publish --dir %s\n", version, repo.Dir, repo.Dir)
			return nil
		},
	}
	repoFlag(cmd, &dir)
	cmd.Flags().StringVar(&channel, "channel", "", "Sets the validity: nightly, main or dev/<branch> [required]")
	_ = cmd.MarkFlagRequired("channel")
	return cmd
}

func newPublishCmd(d deps) *cobra.Command {
	var f struct {
		dir, github, dest string
		dryRun            bool
	}
	cmd := &cobra.Command{
		Use:   "publish --dir <dir>",
		Short: "Upload what cut left: archives to GitHub, metadata to the release host",
		Long: `Upload the last cut or refresh. Before anything is sent the metadata in --dir is
checked as a client would check it, and each archive against the hash the
signed targets name. Then the archives go to the GitHub release for the tag
(created if it is not there; an archive already there is an error), then the
root, targets and snapshot go to the release host with rsync, and the timestamp
last.

Needs gh (logged in, able to upload to the repository) and ssh access to the
release host. --dry-run prints the commands.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := repoFor(f.dir)
			if err != nil {
				return err
			}
			run := d.run
			if run == nil {
				run = releasepub.ExecRunner(cmd.OutOrStdout())
			}
			err = releasepub.Publish(cmd.Context(), releasepub.PublishParams{
				Repo: repo, GitHubRepo: f.github, MetadataDest: f.dest, DryRun: f.dryRun, Now: d.now(), Run: run, Progress: cmd.OutOrStdout(),
			})
			if err != nil {
				return fail("publish", err)
			}
			printf(cmd.OutOrStdout(), "%s\n", published(f.dryRun))
			return nil
		},
	}
	repoFlag(cmd, &f.dir)
	fl := cmd.Flags()
	fl.StringVar(&f.github, "github-repo", releasepub.DefaultGitHubRepo, "The GitHub repository whose releases hold the archives")
	fl.StringVar(&f.dest, "metadata-dest", releasepub.DefaultMetadataDest, "The rsync destination of the metadata on the release host")
	fl.BoolVar(&f.dryRun, "dry-run", false, "Check the directory and print the commands; run nothing")
	return cmd
}

func published(dryRun bool) string {
	if dryRun {
		return "Dry run: nothing was uploaded."
	}
	return "Published."
}
