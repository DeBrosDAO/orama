package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// InsideByIdentity reports whether path is dir or lies inside it, comparing
// dir with path and each of its ancestors by file identity (device and
// inode, os.SameFile), as core/pkg/rwagent's e2e guard does: a symlink, a
// hard-linked directory or a path that differs only in case on a
// case-insensitive file system cannot pass for somewhere else. A dir that
// does not exist holds nothing.
func InsideByIdentity(path, dir string) (bool, error) {
	target, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to stat %s: %w", dir, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("failed to make %s absolute: %w", path, err)
	}
	for p := resolveExisting(abs); ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil && os.SameFile(fi, target) {
			return true, nil
		}
		if p == filepath.Dir(p) {
			return false, nil
		}
	}
}

// resolveExisting resolves the symlinks of the longest part of abs that
// exists and keeps the rest as it is: the ancestors walked are then the
// real ones, not those of a link's own location.
func resolveExisting(abs string) string {
	rest := ""
	for p := abs; ; p = filepath.Dir(p) {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		if p == filepath.Dir(p) {
			return abs
		}
		rest = filepath.Join(filepath.Base(p), rest)
	}
}

// insideEither is within (by name) or InsideByIdentity.
func insideEither(path, dir string) (bool, error) {
	if within(path, dir) {
		return true, nil
	}
	return InsideByIdentity(path, dir)
}
