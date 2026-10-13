package tlsstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// LegacyCaddyStorageDir is where a node's Caddy kept its certificates before
// the shared store: Caddy's file storage under XDG_DATA_HOME=/var/lib/caddy.
const LegacyCaddyStorageDir = "/var/lib/caddy/caddy"

// legacyTrees are the parts of Caddy's file storage worth carrying over: the
// certificates and the ACME account they were obtained with. Locks and OCSP
// staples are not state.
var legacyTrees = []string{"certificates", "acme"}

// legacyFile is one file to import.
type legacyFile struct {
	key, sealed string
	modifiedMS  int64
}

// ImportResult is what ImportLegacy did.
type ImportResult struct {
	// Added is how many keys were stored.
	Added int
	// Skipped names the files that are not CertMagic's (an invalid key, or
	// too large to be a certificate), with why.
	Skipped []string
}

// ImportLegacy seals the files under dir's certificates/ and acme/ into the
// store under the keys Caddy's file storage gave them (their paths below dir).
//
// It runs before the store serves its first call on a node upgraded from a
// release that kept certificates on disk, so the first Caddy to use the store
// finds the certificates the cluster already has instead of ordering new ones
// — which a cluster installed in the last week could not get (five per week
// for one set of names). A dir with no such trees imports nothing.
//
// Each directory is imported whole or not at all, in one statement: every node
// held its own certificate and key for the same name, and a certificate from
// one node beside the key of another is a pair that does not load. A directory
// the store already holds anything under is left as the store has it: the
// first node to import a name wins, and every node then serves that copy.
func (s *Store) ImportLegacy(ctx context.Context, sealKey []byte, dir string) (ImportResult, error) {
	var res ImportResult
	// Every open goes through root, which refuses a path or a symlink that
	// leaves dir: a file swapped for a symlink between the walk and the read
	// cannot make this process seal something outside Caddy's storage.
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("open Caddy's storage %s: %w", dir, err)
	}
	defer root.Close()
	groups := map[string][]legacyFile{}
	for _, tree := range legacyTrees {
		if _, err := root.Lstat(tree); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		// From here a file that vanishes is an error, not an absent tree: the
		// import would otherwise be recorded as done with a name half stored.
		err := fs.WalkDir(root.FS(), tree, func(key string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				res.Skipped = append(res.Skipped, path.Join(dir, key)+": a symlink")
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			f, skip, err := readLegacyFile(root, sealKey, key)
			if err != nil {
				return err
			}
			if skip != "" {
				res.Skipped = append(res.Skipped, path.Join(dir, key)+": "+skip)
				return nil
			}
			parent := path.Dir(f.key)
			groups[parent] = append(groups[parent], f)
			return nil
		})
		if err != nil {
			return res, fmt.Errorf("import Caddy's %s into the TLS store: %w", path.Join(dir, tree), err)
		}
	}
	parents := make([]string, 0, len(groups))
	for p := range groups {
		parents = append(parents, p)
	}
	sort.Strings(parents)
	for _, parent := range parents {
		n, err := s.importDir(ctx, parent, groups[parent])
		if err != nil {
			return res, err
		}
		res.Added += n
	}
	return res, nil
}

// readLegacyFile seals the file at key (a slash path below root). skip says
// why a file that is not CertMagic's was left out; err is one that could not be
// read.
func readLegacyFile(root *os.Root, sealKey []byte, key string) (legacyFile, string, error) {
	if err := ValidKey(key); err != nil {
		return legacyFile{}, err.Error(), nil
	}
	f, err := root.Open(key)
	if err != nil {
		return legacyFile{}, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return legacyFile{}, "", err
	}
	if !info.Mode().IsRegular() {
		return legacyFile{}, "not a regular file", nil
	}
	if info.Size() > MaxValueLen {
		return legacyFile{}, fmt.Sprintf("%d bytes, larger than any certificate", info.Size()), nil
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxValueLen+1))
	if err != nil {
		return legacyFile{}, "", err
	}
	if len(data) > MaxValueLen {
		return legacyFile{}, "grew past the store's limit while being read", nil
	}
	sealed, err := Seal(sealKey, key, data)
	if err != nil {
		return legacyFile{}, "", err
	}
	if len(sealed) > MaxValueLen {
		return legacyFile{}, fmt.Sprintf("%d bytes sealed, over the store's %d", len(sealed), MaxValueLen), nil
	}
	return legacyFile{key: key, sealed: sealed, modifiedMS: info.ModTime().UnixMilli()}, "", nil
}

// importDir stores files, all directly under parent, unless the store already
// holds a key under parent. One statement, so it is one Raft entry: whole or
// nothing, and never interleaved with another node's import of the same name.
func (s *Store) importDir(ctx context.Context, parent string, files []legacyFile) (int, error) {
	rows := make([]string, len(files))
	args := make([]any, 0, len(files)*4+2)
	for i, f := range files {
		rows[i] = "(?, ?, ?, ?)"
		args = append(args, f.key, f.sealed, len(f.sealed), f.modifiedMS)
	}
	args = append(args, under(parent)...)
	query := `INSERT INTO tls_store (key, value, size, modified_unix_ms)
		SELECT column1, column2, column3, column4 FROM (VALUES ` + strings.Join(rows, ", ") + `)
		WHERE NOT EXISTS (SELECT 1 FROM tls_store WHERE substr(key, 1, ?) = ?)`
	res, err := rqlite.SafeExecContext(s.db, ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("import %s into the TLS store: %w", parent, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("import %s into the TLS store: %w", parent, err)
	}
	return int(n), nil
}
