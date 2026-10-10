package releasepub

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/durablefile"
	"github.com/DeBrosOfficial/network/pkg/releasesign"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// Names in a repository directory. The metadata is served as it is; the
// pending record names what the next publish uploads and is never served.
const (
	RootFile      = "root.json"
	TargetsFile   = releaseverify.TargetsFile
	SnapshotFile  = releaseverify.SnapshotFile
	TimestampFile = releaseverify.TimestampFile
	// PendingFile records the archives a cut made that publish has yet to upload.
	PendingFile = "pending-release.json"

	repoDirPerm  = 0o755
	repoFilePerm = 0o644
)

// Repo is a repository directory: the maintainer's working copy of what the
// release host serves.
type Repo struct{ Dir string }

func (r Repo) path(name string) string { return filepath.Join(r.Dir, name) }

// versionedRootName is the file a root version is published as.
func versionedRootName(version int64) string { return fmt.Sprintf("%d.root.json", version) }

// ReadRoot returns root.json, and the parsed root.
func (r Repo) ReadRoot() ([]byte, *metadata.Metadata[metadata.RootType], error) {
	data, err := os.ReadFile(r.path(RootFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("%s has no %s: make the root first (orama maint release init-root): %w", r.Dir, RootFile, fs.ErrNotExist)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", r.path(RootFile), err)
	}
	root, err := metadata.Root().FromBytes(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", r.path(RootFile), err)
	}
	return data, root, nil
}

// loadOptional parses the metadata file name, or returns nil if there is none
// yet (a repository's first release).
func loadOptional[T metadata.Roles](r Repo, name string, empty *metadata.Metadata[T]) (*metadata.Metadata[T], error) {
	data, err := os.ReadFile(r.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", r.path(name), err)
	}
	meta, err := empty.FromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", r.path(name), err)
	}
	return meta, nil
}

// write replaces the named files in dir. Each is replaced atomically; the
// order is the order given, so the timestamp, the entry point a client reads
// first, goes last.
func (r Repo) write(files []namedFile) error {
	if err := os.MkdirAll(r.Dir, repoDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", r.Dir, err)
	}
	for _, f := range files {
		if err := durablefile.Write(r.path(f.name), f.data, repoFilePerm); err != nil {
			return err
		}
	}
	return nil
}

type namedFile struct {
	name string
	data []byte
}

// holdsKey reports whether root lists the key with this id for role.
func holdsKey(root *metadata.Metadata[metadata.RootType], role, keyID string) bool {
	r, ok := root.Signed.Roles[role]
	if !ok {
		return false
	}
	for _, id := range r.KeyIDs {
		if id == keyID {
			_, listed := root.Signed.Keys[id]
			return listed
		}
	}
	return false
}

// checkAgentKey refuses to go on unless this wallet's release key is the one
// root lists for each of roles, so a metadata file is never signed with a key
// the clients do not trust for it.
func checkAgentKey(root *metadata.Metadata[metadata.RootType], pub ed25519.PublicKey, roles ...string) error {
	id, err := KeyID(pub)
	if err != nil {
		return err
	}
	for _, role := range roles {
		if !holdsKey(root, role, id) {
			return fmt.Errorf("this wallet's release key (%s) is not the key the root lists for the %s role: sign with the wallet that made the root", id, role)
		}
	}
	return nil
}

// signStep is one approval: what it is for, announced before the agent is
// asked, then the signature.
type signStep struct {
	n, total int
	what     string
}

func signMetadata[T metadata.Roles](ctx context.Context, agent Agent, pub ed25519.PublicKey, meta *metadata.Metadata[T], step signStep, progress io.Writer) error {
	payload, err := releasesign.Payload(meta)
	if err != nil {
		return err
	}
	if err := CheckSignable(payload); err != nil {
		return fmt.Errorf("%s would be refused by the RootWallet agent: %w", step.what, err)
	}
	if progress != nil {
		fmt.Fprintf(progress, "Approval %d of %d: %s\n  Approve it in the RootWallet desktop app.\n", step.n, step.total, step.what)
	}
	if err := releasesign.Sign(ctx, agent, meta, pub); err != nil {
		return fmt.Errorf("%s: %w", step.what, err)
	}
	return nil
}

// summarize shortens a path list for an approval line.
func summarize(paths []string) string {
	switch len(paths) {
	case 0:
		return "no targets"
	case 1:
		return paths[0]
	}
	return fmt.Sprintf("%s and %d more", paths[0], len(paths)-1)
}
