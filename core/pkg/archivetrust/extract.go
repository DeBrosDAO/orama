package archivetrust

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// extractedDirPerm is every directory Extract makes.
	extractedDirPerm fs.FileMode = 0o755
	// extractedFileMaxPerm caps an extracted file's mode: no setuid, nothing
	// group- or world-writable.
	extractedFileMaxPerm fs.FileMode = 0o755
	// maxExtractedBytes bounds everything one archive may write before it is
	// verified. A real archive is a few hundred megabytes.
	maxExtractedBytes = 4 << 30
	// maxEntries bounds the entries of one archive; a real one has a few
	// dozen.
	maxEntries = 1024
	// maxDepth is the deepest an entry may be: a file in a content directory.
	maxDepth = 2
)

// Extract unpacks the build archive (tar.gz) at src into dest, which must be
// empty. It accepts only regular files and directories below OwnedPaths, at
// most maxDepth deep, each named once: a link, a device, an absolute or
// escaping name, or a duplicate that would let two entries disagree is an
// error. Every mode is set explicitly, whatever the umask. Nothing extracted
// is trusted until VerifyTree says so.
func Extract(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()

	seen := map[string]bool{}
	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the archive: %w", err)
		}
		name, err := entryName(hdr.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		if err := admitEntry(name, hdr.Size, seen, &total); err != nil {
			return err
		}
		if err := extractEntry(tr, hdr, filepath.Join(dest, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
}

// admitEntry applies the per-archive limits to one more entry.
func admitEntry(name string, size int64, seen map[string]bool, total *int64) error {
	if seen[name] {
		return fmt.Errorf("the archive names %s twice", name)
	}
	seen[name] = true
	if len(seen) > maxEntries {
		return fmt.Errorf("the archive has more than %d entries", maxEntries)
	}
	if size > MaxFileBytes {
		return fmt.Errorf("%s is %d bytes, over the %d-byte limit for one archived file", name, size, MaxFileBytes)
	}
	if *total += size; *total > maxExtractedBytes {
		return fmt.Errorf("the archive holds over %d bytes; refusing to write more of it", maxExtractedBytes)
	}
	return nil
}

// entryName is an entry's clean path below the archive root, "" for the root
// itself, or an error for a path outside what an archive installs.
func entryName(raw string) (string, error) {
	name := strings.TrimSuffix(strings.TrimPrefix(raw, "./"), "/")
	if name == "" || name == "." {
		return "", nil
	}
	if path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("the archive entry %q is not a plain relative path", raw)
	}
	if strings.Count(name, "/")+1 > maxDepth {
		return "", fmt.Errorf("the archive entry %q is deeper than an archive's layout", raw)
	}
	top, _, _ := strings.Cut(name, "/")
	if !slices.Contains(OwnedPaths, top) {
		return "", fmt.Errorf("the archive entry %q is outside what an archive installs (%s)",
			raw, strings.Join(OwnedPaths, ", "))
	}
	return name, nil
}

// extractEntry writes one directory or regular file.
func extractEntry(r io.Reader, hdr *tar.Header, dst string) error {
	switch hdr.Typeflag {
	case tar.TypeDir:
		return makeDir(dst)
	case tar.TypeReg:
		if err := makeDir(filepath.Dir(dst)); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(r, hdr.Size)); err != nil {
			out.Close()
			return err
		}
		if err := out.Chmod(hdr.FileInfo().Mode().Perm() & extractedFileMaxPerm); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	default:
		return fmt.Errorf("is of tar type %q; an archive holds only regular files and directories", hdr.Typeflag)
	}
}

// makeDir creates dir (its parent must exist or be created the same way) with
// extractedDirPerm exactly, whatever the umask.
func makeDir(dir string) error {
	if err := os.Mkdir(dir, extractedDirPerm); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", dir)
		}
		return nil
	}
	return os.Chmod(dir, extractedDirPerm)
}

// VerifyArchiveFile extracts the build archive at src into a private
// temporary directory and verifies it against trusted. The temporary copy is
// removed.
func VerifyArchiveFile(src string, trusted []string) (v *Verified, err error) {
	dir, err := os.MkdirTemp("", "orama-verify-")
	if err != nil {
		return nil, fmt.Errorf("create a directory to verify %s in: %w", src, err)
	}
	defer func() { err = errors.Join(err, removeDir(dir)) }()
	if err := Extract(src, dir); err != nil {
		return nil, fmt.Errorf("extract %s: %w", src, err)
	}
	if v, err = VerifyTree(dir, trusted); err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	return v, nil
}

// Upload is a verified archive ready to send to a node that has no verified
// binary of its own yet.
type Upload struct {
	// Path is a tar.gz this package wrote from the verified tree: only the
	// verified files, in a plain layout every tar reads the same way. It is
	// what is uploaded — never the file the operator named, which could be
	// replaced after verification (/tmp is shared), and which another tar
	// might read differently from the one that verified it.
	Path     string
	Verified *Verified
	// Remove deletes the private directory Path is in.
	Remove func() error
}

// PrepareUpload extracts the archive at src into a private directory,
// verifies it against trusted, and writes the verified tree back out as a
// canonical archive.
func PrepareUpload(src string, trusted []string) (u *Upload, err error) {
	dir, err := os.MkdirTemp("", "orama-upload-")
	if err != nil {
		return nil, fmt.Errorf("create a private directory for %s: %w", src, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeDir(dir))
		}
	}()
	tree := filepath.Join(dir, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		return nil, err
	}
	if err := Extract(src, tree); err != nil {
		return nil, fmt.Errorf("extract %s: %w", src, err)
	}
	v, err := VerifyTree(tree, trusted)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	path := filepath.Join(dir, canonicalArchiveName)
	if err := writeCanonical(tree, path); err != nil {
		return nil, fmt.Errorf("write the verified archive: %w", err)
	}
	return &Upload{Path: path, Verified: v, Remove: func() error { return removeDir(dir) }}, nil
}

// canonicalArchiveName is the archive PrepareUpload writes.
const canonicalArchiveName = "archive.tar.gz"

// writeCanonical writes the archive paths of the verified tree at dir to a
// tar.gz at dst: USTAR headers, regular files and directories only, in a fixed
// order, owned by nobody in particular.
func writeCanonical(dir, dst string) (err error) {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, top := range OwnedPaths {
		if err := addCanonical(tw, dir, top); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// addCanonical adds dir/rel — a file, or a directory and the files in it — to
// tw. A missing rel is skipped: not every archive has packages/.
func addCanonical(tw *tar.Writer, dir, rel string) error {
	info, err := lstat(filepath.Join(dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return addCanonicalFile(tw, dir, rel)
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: rel + "/", Mode: int64(extractedDirPerm), Format: tar.FormatUSTAR}); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(dir, rel))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := addCanonicalFile(tw, dir, rel+"/"+e.Name()); err != nil {
			return err
		}
	}
	return nil
}

// addCanonicalFile adds the regular file dir/rel to tw.
func addCanonicalFile(tw *tar.Writer, dir, rel string) error {
	f, info, err := openNoFollow(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	defer f.Close()
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: rel, Mode: int64(info.Mode().Perm() & extractedFileMaxPerm),
		Size: info.Size(), Format: tar.FormatUSTAR}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// removeDir removes a private temporary directory.
func removeDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove %s: %w", dir, err)
	}
	return nil
}
