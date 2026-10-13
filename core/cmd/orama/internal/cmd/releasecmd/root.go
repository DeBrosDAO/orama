package releasecmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releasepub"
)

func newInitRootCmd(d deps) *cobra.Command {
	var dir, keysFile string
	cmd := &cobra.Command{
		Use:   "init-root",
		Short: "Make the repository's root from your RootWallet's release key",
		Long: `Make version 1 of the repository's TUF root: your RootWallet's release public key
for the root, timestamp, snapshot and targets roles, threshold 1, valid for a
year. One approval. Prints the root's SHA-256, the digest network manifests pin
(release_root_sha256), and leaves 1.root.json and root.json in --dir. It refuses
a directory that already has a root.

Today one release key holds all four roles: whoever holds it can sign a root, a
targets file, a snapshot and a timestamp, and it is the only key that can sign
the next root. --keys <file> makes a root that splits the roles when the wallet
can hold more keys, without any other change: a JSON file naming, per role, the
keys (64 hex digits of an ed25519 public key, or "wallet" for your own release
key) and the threshold, for example

  {"root": {"keys": ["wallet"]},
   "targets": {"keys": ["<hex>", "<hex>"], "threshold": 2}}

A role that is left out is your release key at threshold 1. This command signs
the root once, with your key, so your key must be among the root keys and the root
threshold must be 1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := repoFor(dir)
			if err != nil {
				return err
			}
			keys, err := rootKeysFrom(cmd.Context(), d.agent(), keysFile)
			if err != nil {
				return err
			}
			digest, err := releasepub.InitRootWith(cmd.Context(), d.agent(), repo, d.now(), cmd.OutOrStdout(), keys)
			if err != nil {
				return fail("make the root", err)
			}
			printf(cmd.OutOrStdout(), "root sha256: %s\nwritten to %s (root.json and 1.root.json); publish it with: orama maint release publish --dir %s\n", digest, repo.Dir, repo.Dir)
			return nil
		},
	}
	repoFlag(cmd, &dir)
	cmd.Flags().StringVar(&keysFile, "keys", "", "A JSON file naming the keys and threshold of each role (default: your release key for all four roles, threshold 1)")
	return cmd
}

// rootKeysFrom reads the --keys file; without one the layout is today's.
func rootKeysFrom(ctx context.Context, agent releasepub.Agent, path string) (releasepub.RootKeys, error) {
	if path == "" {
		return releasepub.RootKeys{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return releasepub.RootKeys{}, clierr.Usage("--keys: %v", err)
	}
	defer f.Close()
	wallet, err := agent.ReleaseKey(ctx)
	if err != nil {
		return releasepub.RootKeys{}, fail("read the wallet's release key", err)
	}
	keys, err := releasepub.ParseRootKeys(f, wallet)
	if err != nil {
		return releasepub.RootKeys{}, clierr.Usage("--keys %s: %v", path, err)
	}
	return keys, nil
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
			printf(cmd.OutOrStdout(), "root version %d written to %s. Targets and snapshot still expire with the previous root until the next cut; publish it with: orama maint release publish --dir %s\n", version, repo.Dir, repo.Dir)
			return nil
		},
	}
	repoFlag(cmd, &dir)
	return cmd
}
