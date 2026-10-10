package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/sqlguard"
)

// A database image is checked before it is loaded, not after.
//
// Once RQLite has loaded it, every gateway of the namespace serves tenant
// writes against it, so a trigger an image carries fires before any scrub of
// this gateway can run. The check opens the spooled image read-only in SQLite
// itself, the one parser that reads it the way RQLite will, and refuses it when
// it is damaged or holds something that is active or grants access from the
// moment it lands: a trigger, a view over a platform table, an ownership row
// for content only other namespaces hold.
//
// What it accepts and the post-load scrub (scrub.go) removes is passive, so
// real older backups still restore: plaintext api_keys rows (a namespace
// gateway validates keys against the cluster registry and reads none from its
// own database) and ownership rows of other namespaces (every read of the table
// filters on this gateway's own namespace).

// The "sqlite3" driver that opens an image is registered by the gateway
// (pkg/gateway imports github.com/mattn/go-sqlite3). It is not imported here:
// this package's types are also linked into the orama CLI, which is built
// without cgo.

// ErrImageRefused means the image itself is not acceptable: damaged, or
// carrying something a namespace's database may not. It is the caller's
// error (400), unlike a failure to look.
var ErrImageRefused = errors.New("the database image is refused")

// imageCheckTimeout bounds the offline check of one image (quick_check reads
// every page of up to 256 MiB).
const imageCheckTimeout = 3 * time.Minute

// Spool copies r, which may be a request body, to a temporary file of its own
// and returns its path, its size and the function that removes it. A reader
// that errors (a body over its cap, a client that went away) leaves no file.
func Spool(r io.Reader) (path string, size int64, cleanup func(), err error) {
	f, err := os.CreateTemp("", "orama-image-*.db")
	if err != nil {
		return "", 0, nil, fmt.Errorf("create the spool file: %w", err)
	}
	remove := func() { _ = os.Remove(f.Name()) }
	size, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("write the spool file: %w", cerr)
	}
	if err != nil {
		remove()
		return "", 0, nil, err
	}
	return f.Name(), size, remove, nil
}

// checkImageBytes spools an image held in memory (a restore's) and checks it.
func (h *Handler) checkImageBytes(ctx context.Context, image []byte) error {
	path, _, cleanup, err := Spool(bytes.NewReader(image))
	if err != nil {
		return err
	}
	defer cleanup()
	return h.CheckImage(ctx, path)
}

// CheckImage opens the image at path read-only and returns an error wrapping
// ErrImageRefused if it is damaged or holds a trigger, a view the SQL guard
// would refuse as a statement, or an ownership row for content the registry
// records only against other namespaces.
func (h *Handler) CheckImage(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, imageCheckTimeout)
	defer cancel()
	if err := imageStartsAsSQLite(path); err != nil {
		return err
	}
	// immutable: the file is never written, journaled or locked.
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return fmt.Errorf("open the database image: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// The image is untrusted: nothing in its schema may run while it is read.
	if _, err := db.ExecContext(ctx, "PRAGMA trusted_schema = OFF"); err != nil {
		return refuse("it is not a readable SQLite database: %v", err)
	}
	if err := imageIsIntact(ctx, db); err != nil {
		return err
	}
	if err := imageHasNoPlatformSQL(ctx, db); err != nil {
		return err
	}
	return h.imageGrantsNoForeignContent(ctx, db)
}

// imageStartsAsSQLite refuses what is not a database file at all. SQLite
// itself reads an empty file as an empty database.
func imageStartsAsSQLite(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open the database image: %w", err)
	}
	defer f.Close()
	head := make([]byte, len(nsbackup.SQLiteMagic))
	if _, err := io.ReadFull(f, head); err != nil || string(head) != nsbackup.SQLiteMagic {
		return refuse("it is not a SQLite database file")
	}
	return nil
}

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrImageRefused, fmt.Sprintf(format, args...))
}

func imageIsIntact(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return refuse("it is not a readable SQLite database: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return refuse("it is not a readable SQLite database: %v", err)
		}
		if line != "ok" {
			return refuse("it is damaged (quick_check: %s)", line)
		}
	}
	if err := rows.Err(); err != nil {
		return refuse("it is not a readable SQLite database: %v", err)
	}
	return nil
}

func imageHasNoPlatformSQL(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, platformSQLQuery)
	if err != nil {
		return refuse("its schema cannot be read: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, name string
		var def sql.NullString
		if err := rows.Scan(&kind, &name, &def); err != nil {
			return refuse("its schema cannot be read: %v", err)
		}
		if kind == "trigger" {
			return refuse("it has a trigger (%s); a namespace's database has none", name)
		}
		if sqlguard.Check(def.String) != nil {
			return refuse("it has a view (%s) over a platform table", name)
		}
	}
	return rows.Err()
}

// imageTable returns the name a table has in the image's schema. SQLite
// resolves table names case-insensitively, so a table the platform reads as
// ipfs_content_ownership may be stored as IPFS_Content_Ownership; matching the
// name in the schema byte for byte would let such an image skip every check
// and be live after the load.
func imageTable(ctx context.Context, db *sql.DB, table string) (name string, found bool, err error) {
	err = db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ? COLLATE NOCASE", table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, refuse("its schema cannot be read: %v", err)
	}
	return name, true, nil
}

// imageHasColumns refuses a table that lacks a column the platform reads it by.
// The shipped schema has had both since migration 008; a table without them is
// not one this gateway can read, and a load would leave storage failing on
// "no such column" for the namespace.
func imageHasColumns(ctx context.Context, db *sql.DB, table string, want ...string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quoteIdent(table)+")")
	if err != nil {
		return refuse("the columns of %s cannot be read: %v", table, err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return refuse("the columns of %s cannot be read: %v", table, err)
		}
		have[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return refuse("the columns of %s cannot be read: %v", table, err)
	}
	for _, w := range want {
		if !have[w] {
			return refuse("its %s table has no %q column, so this gateway could not read it", table, w)
		}
	}
	return nil
}

// imageGrantsNoForeignContent refuses an image whose ownership rows for this
// namespace name content the registry records only against other namespaces.
// The storage handlers read exactly those rows to decide who may read a CID
// (authorize.go, download_handler.go), on every gateway of the namespace, so a
// row like that grants access from the moment the load lands.
func (h *Handler) imageGrantsNoForeignContent(ctx context.Context, db *sql.DB) error {
	table, ok, err := imageTable(ctx, db, "ipfs_content_ownership")
	if err != nil || !ok {
		return err
	}
	if err := imageHasColumns(ctx, db, table, "cid", "namespace"); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, "SELECT cid FROM "+quoteIdent(table)+" WHERE namespace = ?", h.cfg.Namespace)
	if err != nil {
		return refuse("its ipfs_content_ownership table cannot be read: %v", err)
	}
	defer rows.Close()
	var cids []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return refuse("its ipfs_content_ownership table cannot be read: %v", err)
		}
		if c != "" {
			cids = append(cids, c)
		}
	}
	if err := rows.Err(); err != nil {
		return refuse("its ipfs_content_ownership table cannot be read: %v", err)
	}
	elsewhere, err := h.heldElsewhereOnly(ctx, cids)
	if err != nil {
		return err
	}
	if len(elsewhere) > 0 {
		return refuse("its ipfs_content_ownership table names %d CIDs that only other namespaces hold (first: %s)", len(elsewhere), elsewhere[0])
	}
	return nil
}
