// Command testtuf makes and updates a TUF release repository signed by
// software keys, for the stagenet test root and for local tries of
// `orama node setup --release` and the auto-update agent.
//
// The keys it makes sit in a directory on this machine. A production release
// root is signed by release signers through their RootWallets; this tool is
// not that ceremony and its roots must never be adopted by a production
// cluster.
//
//	testtuf init    -dir D                       a root, keys and an empty release list
//	testtuf publish -dir D -channel stable -archive orama-0.3.1-linux-amd64.tar.gz
//	testtuf refresh -dir D                       fresh timestamp and snapshot
//
// D/repo is what a web server serves; D/repo/root.json is the root an
// operator is given out of band; D/keys holds the private keys.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

const (
	defaultRootValidity      = 365 * 24 * time.Hour
	defaultTimestampValidity = 7 * 24 * time.Hour
	repoSubdir               = "repo"
	keysSubdir               = "keys"
	targetsSubdir            = "targets"
	repoDirPerm              = 0o755
	repoFilePerm             = 0o644
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "testtuf:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: testtuf init|publish|refresh -dir D [flags]")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dir := fs.String("dir", "", "the repository's working directory")
	channel := fs.String("channel", "stable", "publish: the channel to add the archive to")
	archive := fs.String("archive", "", "publish: an orama-<version>-linux-<arch>.tar.gz to add")
	rootValid := fs.Duration("root-valid", defaultRootValidity, "init: how long the root and the roles under it are valid")
	tsValid := fs.Duration("timestamp-valid", defaultTimestampValidity, "how long the timestamp is valid; refresh before it ends")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("-dir is required")
	}
	switch args[0] {
	case "init":
		return initRepo(*dir, *rootValid, *tsValid)
	case "publish":
		return publish(*dir, *channel, *archive, *tsValid)
	case "refresh":
		return refresh(*dir, *tsValid)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func initRepo(dir string, rootValid, tsValid time.Duration) error {
	repo := filepath.Join(dir, repoSubdir)
	if err := os.MkdirAll(repo, repoDirPerm); err != nil {
		return err
	}
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		return err
	}
	if err := keys.Save(filepath.Join(dir, keysSubdir)); err != nil {
		return err
	}
	until := time.Now().Add(rootValid)
	root, err := releaserepo.NewRoot(keys, until)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repo, releaserepo.RootFile), root, repoFilePerm); err != nil {
		return err
	}
	if err := write(repo, keys, 1, until, tsValid); err != nil {
		return err
	}
	fmt.Printf("root %s\n", releaseverify.RootDigest(root))
	fmt.Printf("give operators %s out of band; serve %s\n", filepath.Join(repo, releaserepo.RootFile), repo)
	return nil
}

func publish(dir, channel, archive string, tsValid time.Duration) error {
	if archive == "" {
		return fmt.Errorf("publish needs -archive")
	}
	name := filepath.Base(archive)
	if _, err := releaseverify.ParseArchiveTarget(channel + "/" + name); err != nil {
		return err
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, repoSubdir, targetsSubdir, channel, name)
	if err := os.MkdirAll(filepath.Dir(dst), repoDirPerm); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, repoFilePerm); err != nil {
		return err
	}
	return refresh(dir, tsValid)
}

// refresh re-signs all metadata at the next version, listing every archive
// under targets/.
func refresh(dir string, tsValid time.Duration) error {
	repo := filepath.Join(dir, repoSubdir)
	keys, err := releaserepo.LoadKeys(filepath.Join(dir, keysSubdir))
	if err != nil {
		return err
	}
	version, until, err := nextVersion(repo)
	if err != nil {
		return err
	}
	return write(repo, keys, version, until, tsValid)
}

// write builds the metadata for version from the archives under repo/targets
// (<channel>/<file>, a channel being one or two directories) and writes it.
func write(repo string, keys releaserepo.Keys, version int64, until time.Time, tsValid time.Duration) error {
	targets := map[string][]byte{}
	root := filepath.Join(repo, targetsSubdir)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tar.gz") {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := releaseverify.ParseArchiveTarget(filepath.ToSlash(rel)); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		targets[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	files, err := releaserepo.Build(keys, releaserepo.Spec{
		Version: version, RootValidUntil: until, TimestampExpires: time.Now().Add(tsValid), Targets: targets,
	})
	if err != nil {
		return err
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(repo, name), data, repoFilePerm); err != nil {
			return err
		}
	}
	fmt.Printf("wrote metadata version %d\n", version)
	return nil
}

// nextVersion is one above the repository's current timestamp version, and the
// time the root expires, which the other roles keep.
func nextVersion(repo string) (int64, time.Time, error) {
	ts, err := metadata.Timestamp(time.Time{}).FromFile(filepath.Join(repo, releaserepo.TimestampFile))
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("read the current timestamp (was the repository initialised?): %w", err)
	}
	root, err := metadata.Root(time.Time{}).FromFile(filepath.Join(repo, releaserepo.RootFile))
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("read the root: %w", err)
	}
	return ts.Signed.Version + 1, root.Signed.Expires, nil
}
