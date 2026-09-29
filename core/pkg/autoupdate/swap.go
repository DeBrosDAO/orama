package autoupdate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Suffixes of the files a swap keeps beside the live binary. Both live in
// the live binary's directory, so every rename stays on one filesystem and
// is atomic.
const (
	// NextSuffix is the new binary, written and synced before the swap.
	NextSuffix = ".next"
	// PrevSuffix is the binary the swap replaced. It is kept after a
	// successful upgrade so the previous release can be put back.
	PrevSuffix = ".prev"
	// newBinaryPerm is the mode of a binary that replaces nothing.
	newBinaryPerm fs.FileMode = 0o755
)

// FileSwap replaces Live with Next, a file the caller has already verified.
type FileSwap struct {
	Live string
	Next string
}

// Steps are the two reversible steps of one swap: stage copies Next beside
// Live, and swap renames it over Live after keeping Live as Live+PrevSuffix.
// Live is never absent: the previous binary is hard-linked aside before the
// rename replaces it.
func (f FileSwap) Steps() []Step {
	name := filepath.Base(f.Live)
	return []Step{
		{Name: "stage " + name, Do: f.stage, Undo: f.unstage},
		{Name: "swap " + name, Do: f.swap, Undo: f.restore},
	}
}

func (f FileSwap) next() string { return f.Live + NextSuffix }
func (f FileSwap) prev() string { return f.Live + PrevSuffix }

// stage writes the new binary beside the live one with the live one's mode
// and owner and syncs it, so the rename that follows publishes complete bytes.
func (f FileSwap) stage() error {
	if f.Live == "" || f.Next == "" {
		return fmt.Errorf("a swap needs both the live path and the new file")
	}
	info, err := os.Stat(f.Live)
	if errors.Is(err, fs.ErrNotExist) {
		if err := removeIfPresent(f.next()); err != nil {
			return err
		}
		return copyFile(f.Next, f.next(), newBinaryPerm)
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", f.Live, err)
	}
	if err := removeIfPresent(f.next()); err != nil {
		return err
	}
	if err := copyFile(f.Next, f.next(), info.Mode().Perm()); err != nil {
		return err
	}
	if err := chownLike(f.next(), info); err != nil {
		return errors.Join(err, os.Remove(f.next()))
	}
	return nil
}

func (f FileSwap) unstage() error { return removeIfPresent(f.next()) }

// swap keeps the live binary as prev, then renames next over live.
func (f FileSwap) swap() error {
	if err := removeIfPresent(f.prev()); err != nil {
		return err
	}
	if _, err := os.Lstat(f.Live); err == nil {
		if err := os.Link(f.Live, f.prev()); err != nil {
			return fmt.Errorf("keep %s as %s: %w", f.Live, f.prev(), err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", f.Live, err)
	}
	if err := os.Rename(f.next(), f.Live); err != nil {
		return fmt.Errorf("rename %s over %s: %w", f.next(), f.Live, err)
	}
	return nil
}

// restore renames prev back over live, or removes live when the swap
// replaced nothing.
func (f FileSwap) restore() error {
	if _, err := os.Lstat(f.prev()); errors.Is(err, fs.ErrNotExist) {
		return removeIfPresent(f.Live)
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", f.prev(), err)
	}
	if err := os.Rename(f.prev(), f.Live); err != nil {
		return fmt.Errorf("put %s back over %s: %w", f.prev(), f.Live, err)
	}
	return nil
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// copyFile writes src to a new file dst with perm and syncs it.
func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	_, cerr := io.Copy(out, in)
	serr := out.Sync()
	xerr := out.Close()
	// Chmod: the umask must not decide what the services may run.
	if err := errors.Join(cerr, serr, xerr, os.Chmod(dst, perm)); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", dst, err), os.Remove(dst))
	}
	return nil
}
