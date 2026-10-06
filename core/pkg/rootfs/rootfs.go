// Package rootfs is how code running as root touches files below a directory
// only root may write — the anchor, /opt/orama on a node — without following a
// symlink.
//
// `orama node install` and `orama node upgrade` run as root, but most of what
// they write lives under /opt/orama/.orama, which belongs to the unprivileged
// orama user that runs the node and every gateway. Before this package, root
// wrote there with os.WriteFile and friends, which follow symlinks: a
// compromised orama process could replace configs/node.yaml with a symlink to
// /etc/sudoers.d/x and have the next upgrade write it as root.
//
// Every path is resolved one component at a time from a descriptor of the
// anchor, each component opened with O_NOFOLLOW relative to the one before it,
// so no component is ever looked up by path again and none can be swapped for
// a symlink between a check and a use. A symlink anywhere below the anchor is
// an error naming the path; nothing is replaced or followed. The component
// walk is used rather than Linux's openat2(RESOLVE_NO_SYMLINKS) because it
// behaves identically on darwin, so the tests run on a developer's machine,
// and it cannot be refused by a seccomp profile that predates openat2.
//
// The anchor itself is opened normally: its own path is trusted because only
// root can write it. When running as root, an anchor that is not owned by root
// or is writable by its group or others is refused, since everything below it
// would then be as untrusted as the orama tree.
package rootfs

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// ErrSymlink is wrapped by every error that stopped at a symlink below the
// anchor.
var ErrSymlink = errors.New("is a symlink, which root does not follow here")

// SmallFileLimit bounds a read of a config or secret file — node.yaml, a
// secret, a service's JSON config — which are kilobytes. A larger file in the
// orama tree is refused rather than loaded into a root process.
const SmallFileLimit = 1 << 20

// anchorPerm is the mode MkdirAll creates a missing anchor with: the services
// may traverse it, only root may write it.
const anchorPerm fs.FileMode = 0o755

// writableByOthers are the mode bits that let someone other than the owner
// add, remove or rename entries in a directory.
const writableByOthers = 0o022

// Root is an anchor: a directory only root may write, below which rootfs
// resolves paths without following symlinks.
type Root struct {
	dir string
}

// At is the anchor dir. Paths given to its methods must be absolute and inside
// dir.
func At(dir string) Root {
	return Root{dir: filepath.Clean(dir)}
}

// Dir is the anchor's path.
func (r Root) Dir() string {
	return r.dir
}

// components splits path into its components below the anchor. The anchor
// itself has none.
func (r Root) components(path string) ([]string, error) {
	if !filepath.IsAbs(r.dir) {
		return nil, fmt.Errorf("anchor %q is not an absolute path", r.dir)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%q is not an absolute path", path)
	}
	rel, err := filepath.Rel(r.dir, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s is not below %s", path, r.dir)
	}
	if rel == "." {
		return nil, nil
	}
	return strings.Split(rel, string(filepath.Separator)), nil
}

// leafComponents is components for an operation on a file below the anchor,
// which the anchor itself is not.
func (r Root) leafComponents(path string) ([]string, error) {
	comps, err := r.components(path)
	if err != nil {
		return nil, err
	}
	if len(comps) == 0 {
		return nil, fmt.Errorf("%s is the anchor itself, not a path below it", path)
	}
	return comps, nil
}

// checkAnchor refuses, for a root process, an anchor someone other than root
// could change: then a symlink planted in it would be followed when the anchor
// is opened.
func checkAnchor(dir string, uid, mode uint32, euid int) error {
	if euid != 0 {
		return nil
	}
	if uid != 0 {
		return fmt.Errorf("%s is owned by uid %d, not root; everything below it is untrusted, so root refuses to write there (chown root %s)", dir, uid, dir)
	}
	if mode&writableByOthers != 0 {
		return fmt.Errorf("%s is writable by its group or others (mode %#o); everything below it is untrusted, so root refuses to write there (chmod go-w %s)", dir, mode&0o7777, dir)
	}
	return nil
}
