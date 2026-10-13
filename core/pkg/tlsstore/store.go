package tlsstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

const (
	// MaxKeyLen bounds a stored key or lock name. CertMagic's keys are a CA,
	// a name and a file — a few hundred bytes at most.
	MaxKeyLen = 1024

	// MaxValueLen bounds a sealed value. A certificate chain, its key and its
	// metadata are a few kilobytes; CertMagic's storage is not for large files.
	MaxValueLen = 256 << 10

	// MaxLease bounds how long a lock may be held without a renewal. CertMagic
	// asks for the time one obtain attempt may take, which is minutes.
	MaxLease = 2 * time.Hour
)

// ErrNotExist is returned for a key the store does not hold.
var ErrNotExist = errors.New("not in the TLS store")

// KeyInfo describes a stored key, or a directory of them.
type KeyInfo struct {
	Key        string
	Modified   time.Time
	Size       int64
	IsTerminal bool
}

// Store is the store's table in the cluster registry.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns the store kept in db, which must be the cluster registry.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

// ValidKey reports why key cannot name a stored value or a lock, or nil. Keys
// are slash-separated paths with no empty, "." or ".." component.
func ValidKey(key string) error {
	if key == "" || len(key) > MaxKeyLen {
		return fmt.Errorf("key must be 1 to %d bytes", MaxKeyLen)
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e || r == '\\' {
			return fmt.Errorf("key %q holds a character outside printable ASCII, or a backslash", key)
		}
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("key %q has an empty, \".\" or \"..\" component", key)
		}
	}
	return nil
}

// Load returns the sealed value stored at key.
func (s *Store) Load(ctx context.Context, key string) (string, error) {
	rows, err := rqlite.SafeQueryContext(s.db, ctx, `SELECT value FROM tls_store WHERE key = ?`, key)
	if err != nil {
		return "", fmt.Errorf("read %s from the TLS store: %w", key, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("read %s from the TLS store: %w", key, err)
		}
		return "", ErrNotExist
	}
	var value string
	if err := rows.Scan(&value); err != nil {
		return "", fmt.Errorf("read %s from the TLS store: %w", key, err)
	}
	return value, nil
}

// Put stores a sealed value at key, replacing what was there.
func (s *Store) Put(ctx context.Context, key, value string) error {
	_, err := rqlite.SafeExecContext(s.db, ctx,
		`INSERT INTO tls_store (key, value, size, modified_unix_ms) VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, size = excluded.size, modified_unix_ms = excluded.modified_unix_ms`,
		key, value, len(value), s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("write %s to the TLS store: %w", key, err)
	}
	return nil
}

// Delete removes key and, when it is a directory, every key under it.
// Deleting a key that is not there is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := rqlite.SafeExecContext(s.db, ctx,
		`DELETE FROM tls_store WHERE key = ? OR substr(key, 1, ?) = ?`, append([]any{key}, under(key)...)...)
	if err != nil {
		return fmt.Errorf("delete %s from the TLS store: %w", key, err)
	}
	return nil
}

// Stat describes key: a stored value, or a directory when only keys under it
// are stored.
func (s *Store) Stat(ctx context.Context, key string) (KeyInfo, error) {
	rows, err := rqlite.SafeQueryContext(s.db, ctx,
		`SELECT key, size, modified_unix_ms FROM tls_store WHERE key = ? OR substr(key, 1, ?) = ? ORDER BY key = ? DESC LIMIT 1`,
		key, len(key)+1, key+"/", key)
	if err != nil {
		return KeyInfo{}, fmt.Errorf("stat %s in the TLS store: %w", key, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return KeyInfo{}, fmt.Errorf("stat %s in the TLS store: %w", key, err)
		}
		return KeyInfo{}, ErrNotExist
	}
	var (
		found          string
		rawSize, rawMS any
	)
	if err := rows.Scan(&found, &rawSize, &rawMS); err != nil {
		return KeyInfo{}, fmt.Errorf("stat %s in the TLS store: %w", key, err)
	}
	size, err := integer(rawSize)
	if err != nil {
		return KeyInfo{}, fmt.Errorf("stat %s in the TLS store: size: %w", key, err)
	}
	msec, err := integer(rawMS)
	if err != nil {
		return KeyInfo{}, fmt.Errorf("stat %s in the TLS store: modified_unix_ms: %w", key, err)
	}
	if found != key {
		return KeyInfo{Key: key}, nil
	}
	return KeyInfo{Key: key, Size: size, Modified: time.UnixMilli(msec), IsTerminal: true}, nil
}

// List returns the keys under prefix ("" for all). Recursive lists every
// stored key; otherwise each entry directly under prefix, file or directory,
// is listed once.
func (s *Store) List(ctx context.Context, prefix string, recursive bool) ([]string, error) {
	query, args := `SELECT key FROM tls_store`, []any{}
	if prefix != "" {
		query, args = query+` WHERE substr(key, 1, ?) = ?`, append(args, under(prefix)...)
	}
	rows, err := rqlite.SafeQueryContext(s.db, ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %q in the TLS store: %w", prefix, err)
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("list %q in the TLS store: %w", prefix, err)
		}
		if !recursive {
			key = childOf(prefix, key)
		}
		seen[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %q in the TLS store: %w", prefix, err)
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// childOf is the entry directly under prefix that key lies in.
func childOf(prefix, key string) string {
	rest := key
	if prefix != "" {
		rest = strings.TrimPrefix(key, prefix+"/")
	}
	first, _, _ := strings.Cut(rest, "/")
	if prefix == "" {
		return first
	}
	return prefix + "/" + first
}

// under is the arguments of `substr(key, 1, ?) = ?` matching every key under
// dir. A prefix compare rather than LIKE: LIKE folds ASCII case, so
// "certificates/Foo" would also match "certificates/foo/…".
func under(dir string) []any {
	return []any{len(dir) + 1, dir + "/"}
}

// integer reads an INTEGER column. RQLite's driver hands every number over as
// a float64 — a millisecond timestamp arrives as 1.79092464079e+12, which
// database/sql will not scan into an int64 — while SQLite's hands over an
// int64. A float64 holds every integer this store writes (sizes, and
// millisecond timestamps, far below 2^53) exactly.
func integer(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case float64:
		if n != math.Trunc(n) || math.Abs(n) > 1<<53 {
			return 0, fmt.Errorf("%v is not an integer", n)
		}
		return int64(n), nil
	default:
		return 0, fmt.Errorf("%T is not a number", v)
	}
}
