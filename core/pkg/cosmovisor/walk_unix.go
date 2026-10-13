//go:build unix

package cosmovisor

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	// dirFlags open one directory, refusing a symlink.
	dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	// rootOnlyDirPerm is genesis/, upgrades/ and everything below them.
	rootOnlyDirPerm = 0o755
	// cosmovisorDirPerm is cosmovisor/: group orama-chain may add its own
	// entries (current), and the sticky bit stops it removing root's.
	cosmovisorDirPerm = 0o775 | unix.S_ISVTX
	// writableByOthers are the mode bits that let someone other than the
	// owner add, remove or rename entries.
	writableByOthers = 0o022
)

// openHome opens the chain home one component at a time from /. Every
// ancestor must be root's and not writable by others (a sticky directory
// such as /tmp excepted). The home itself must be a real directory owned
// by root or the chain account.
func (l Layout) openHome() (int, error) {
	fd, err := unix.Open("/", dirFlags, 0)
	if err != nil {
		return -1, fmt.Errorf("open /: %w", err)
	}
	comps := strings.Split(strings.TrimPrefix(l.Home, "/"), "/")
	for i, name := range comps {
		next, err := unix.Openat(fd, name, dirFlags, 0)
		unix.Close(fd)
		if err != nil {
			return -1, fmt.Errorf("open %s without following a symlink: %w", l.Home, err)
		}
		fd = next
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			unix.Close(fd)
			return -1, fmt.Errorf("stat %s: %w", name, err)
		}
		if i == len(comps)-1 {
			if !l.isTrusted(st.Uid, name) && int(st.Uid) != l.ChainUID {
				unix.Close(fd)
				return -1, fmt.Errorf("the chain home %s is owned by uid %d, not root or the chain account", l.Home, st.Uid)
			}
			continue
		}
		sticky := st.Mode&unix.S_ISVTX != 0
		if !l.isTrusted(st.Uid, name) || (st.Mode&writableByOthers != 0 && !sticky) {
			unix.Close(fd)
			return -1, fmt.Errorf("%s: %s must be root's and writable only by root (uid %d, mode %o)", l.Home, name, st.Uid, st.Mode&0o7777)
		}
	}
	return fd, nil
}

// openRoot opens cosmovisor/ under the home, creating it, and sets it to
// root:<chain group> 1775.
func (l Layout) openRoot(homeFD int) (int, error) {
	fd, err := l.openDir(homeFD, rootDir, true, true)
	if err != nil {
		return -1, err
	}
	if err := unix.Fchown(fd, -1, l.ChainGID); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("give %s to group %d: %w", l.Root(), l.ChainGID, err)
	}
	if err := unix.Fchmod(fd, cosmovisorDirPerm); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("chmod %s: %w", l.Root(), err)
	}
	return fd, nil
}

// openDir opens name below parent without following a symlink, creating it
// when create is set, and refuses one root does not own or others may
// write. stickyOK accepts a group-writable sticky directory (cosmovisor/).
func (l Layout) openDir(parent int, name string, create, stickyOK bool) (int, error) {
	if create {
		if err := unix.Mkdirat(parent, name, rootOnlyDirPerm); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("create %s: %w", name, err)
		}
	}
	fd, err := unix.Openat(parent, name, dirFlags, 0)
	if err != nil {
		return -1, fmt.Errorf("open %s without following a symlink: %w", name, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("stat %s: %w", name, err)
	}
	writable := st.Mode&writableByOthers != 0
	if stickyOK && st.Mode&unix.S_ISVTX != 0 {
		writable = st.Mode&0o002 != 0
	}
	if !l.isTrusted(st.Uid, name) || writable {
		unix.Close(fd)
		return -1, fmt.Errorf("%s must be root's and writable only by root (uid %d, mode %o)", name, st.Uid, st.Mode&0o7777)
	}
	// Set, not left to the umask: the chain account must traverse it.
	if !stickyOK {
		if err := unix.Fchmod(fd, rootOnlyDirPerm); err != nil {
			unix.Close(fd)
			return -1, fmt.Errorf("chmod %s: %w", name, err)
		}
	}
	return fd, nil
}

// refuseExisting refuses to stage over an entry that is already there.
func refuseExisting(dirFD int, name, path string) error {
	var st unix.Stat_t
	err := unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return fmt.Errorf("%s is already staged; remove it first to stage a different binary", path)
	}
	if !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return nil
}

// closeAll closes descriptors, reporting the first failure.
func closeAll(fds ...int) error {
	var errs []error
	for _, fd := range fds {
		if fd >= 0 {
			errs = append(errs, unix.Close(fd))
		}
	}
	return errors.Join(errs...)
}
