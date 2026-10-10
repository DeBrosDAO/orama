package releasecmd

import (
	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/pkg/releasepub"
)

func newInitRootCmd(d deps) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "init-root",
		Short: "Make the repository's root from your RootWallet's release key",
		Long: `Make version 1 of the repository's TUF root: your RootWallet's release public key
for the root, timestamp, snapshot and targets roles, threshold 1, valid for a
year. One approval. Prints the root's SHA-256, the digest network manifests pin
(release_root_sha256), and leaves 1.root.json and root.json in --dir. It refuses
a directory that already has a root.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := repoFor(dir)
			if err != nil {
				return err
			}
			digest, err := releasepub.InitRoot(cmd.Context(), d.agent(), repo, d.now(), cmd.OutOrStdout())
			if err != nil {
				return fail("make the root", err)
			}
			printf(cmd.OutOrStdout(), "root sha256: %s\nwritten to %s (root.json and 1.root.json); publish it with: orama maint release publish --dir %s\n", digest, repo.Dir, repo.Dir)
			return nil
		},
	}
	repoFlag(cmd, &dir)
	return cmd
}

func newRenewRootCmd(d deps) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "renew-root",
		Short: "Make the next version of the root with a new expiry",
		Long: `Make the next version of the root: the same keys, a new year of validity. One
approval. Clients that hold the previous root fetch <N+1>.root.json, verify it
against the root they hold and adopt it, so nobody is handed a new file. Do it
before the root expires; an expired root stops every release.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := repoFor(dir)
			if err != nil {
				return err
			}
			version, err := releasepub.RenewRoot(cmd.Context(), d.agent(), repo, d.now(), cmd.OutOrStdout())
			if err != nil {
				return fail("renew the root", err)
			}
			printf(cmd.OutOrStdout(), "root version %d written to %s; publish it with: orama maint release publish --dir %s\n", version, repo.Dir, repo.Dir)
			return nil
		},
	}
	repoFlag(cmd, &dir)
	return cmd
}
