package indexer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cockroachdb/pebble"
)

// movedAsideTimeFormat is the timestamp in the name an old index is moved to (UTC, sortable).
const movedAsideTimeFormat = "20060102T150405Z"

// pebbleCurrent is the file every Pebble directory has; a directory without it holds no index.
const pebbleCurrent = "CURRENT"

// MoveResult is what MoveAsideStale found and did.
type MoveResult struct {
	// Present is whether dir holds an index.
	Present bool
	// Version is the layout version of the index, 0 for one written before indexes were versioned.
	Version uint64
	// Current is the layout version this indexer writes.
	Current uint64
	// MovedTo is where the index was moved to; empty when it was kept.
	MovedTo string
}

// MoveAsideStale moves the index in dir aside when it was written in an older layout than this
// indexer's, so the indexer starts a new one: an index cannot be brought up to a layout that
// computes more as blocks are ingested (Open refuses it). The old index is renamed, with its
// layout version and the time, next to where it was; nothing is deleted, and it is not read again.
// An index of the current layout is left as it is, and so is the absence of one. An index of a
// layout newer than this indexer's is an error: the indexer was rolled back, and the newer index
// is not this indexer's to move.
//
// The index is opened read-only, so it must not be open in another process (the indexer's unit is
// stopped) and nothing is written to it.
//
// The indexer's account owns the directory, so it can put anything at its path. dir must be a real
// directory and its CURRENT a regular file (checked with Lstat, which does not follow a link): a
// symlink there would make this open, and then rename, whatever it points at. The upgrade runs this
// as the indexer's account, so a swap between the check and the open gains the account nothing it
// does not have; the check refuses the obvious plant with a message instead of reading through it.
func MoveAsideStale(dir string, now time.Time) (MoveResult, error) {
	res := MoveResult{Current: schemaVersion}
	present, err := indexPresent(dir)
	if err != nil || !present {
		return res, err
	}
	res.Present = true
	have, err := layoutOf(dir)
	if err != nil {
		return res, err
	}
	res.Version = have
	switch {
	case have == schemaVersion:
		return res, nil
	case have > schemaVersion:
		return res, fmt.Errorf("the index in %s has layout version %d, newer than the %d this indexer writes: the indexer was rolled back; run the release that wrote it, or move the index aside yourself", dir, have, schemaVersion)
	}
	dst := fmt.Sprintf("%s.v%d-%s", dir, have, now.UTC().Format(movedAsideTimeFormat))
	if _, err := os.Lstat(dst); err == nil {
		return res, fmt.Errorf("failed to move the index %s aside: %s exists", dir, dst)
	}
	if err := os.Rename(dir, dst); err != nil {
		return res, fmt.Errorf("failed to move the index %s of layout version %d aside to %s: %w", dir, have, dst, err)
	}
	res.MovedTo = dst
	return res, nil
}

// indexPresent reports whether dir holds an index, and refuses what is not one: a dir or a CURRENT
// that is a symlink, a dir that is not a directory, a CURRENT that is not a regular file. An absent
// dir, or one without a CURRENT, holds no index.
func indexPresent(dir string) (bool, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to look for an index in %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("refusing %s: it is a symbolic link, not the index directory; the indexer's account owns it, so someone may have replaced the index with a link", dir)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("refusing %s: it is not a directory", dir)
	}
	info, err = os.Lstat(filepath.Join(dir, pebbleCurrent))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to look for an index in %s: %w", dir, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("refusing %s: %s is not a regular file", dir, pebbleCurrent)
	}
	return true, nil
}

// layoutOf reads the layout version an index in dir was written with, opening it read-only. Open
// stamps the version on every index it creates, so an index without one was written before indexes
// were versioned: version 0.
func layoutOf(dir string) (uint64, error) {
	db, err := pebble.Open(dir, &pebble.Options{ReadOnly: true})
	if err != nil {
		return 0, fmt.Errorf("failed to read the layout version of the index %s (is the indexer still running?): %w", dir, err)
	}
	have, ok, err := getUint(db, []byte(keyVersion))
	if cerr := db.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("failed to close the index %s: %w", dir, cerr)
	}
	if err != nil || ok {
		return have, err
	}
	return 0, nil
}
