package ns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Reserve serves waiters in arrival order: each takes a numbered ticket (a
// flock'd file in the slot dir, released with its descriptor when the
// process dies) and tries for slots only while no live earlier ticket
// exists. A waiter for several slots is therefore never starved by a
// stream of waiters for one.
const (
	ticketPrefix = "ticket-"
	ticketSuffix = ".lock"
	counterName  = "tickets.next"
	guardName    = "guard.lock"
)

// ticket is one waiter's place in the queue.
type ticket struct {
	n    uint64
	file *os.File
}

// takeTicket draws the next ticket number under the guard lock and holds
// its file locked.
func takeTicket(dir string) (*ticket, error) {
	guard, err := lockFile(filepath.Join(dir, guardName), syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	n, err := nextTicketNumber(dir)
	if err != nil {
		return nil, err
	}
	f, err := lockFile(ticketPath(dir, n), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return nil, fmt.Errorf("failed to take namespace slot ticket %d: %w", n, err)
	}
	return &ticket{n: n, file: f}, nil
}

// nextTicketNumber reads and advances the counter file; the guard is held.
func nextTicketNumber(dir string) (uint64, error) {
	path := filepath.Join(dir, counterName)
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("failed to read the namespace ticket counter %s: %w", path, err)
	}
	var n uint64
	if s := strings.TrimSpace(string(raw)); s != "" {
		if n, err = strconv.ParseUint(s, 10, 64); err != nil {
			return 0, fmt.Errorf("the namespace ticket counter %s holds %q: %w", path, s, err)
		}
	}
	if err := os.WriteFile(path, []byte(strconv.FormatUint(n+1, 10)), slotFileMode); err != nil {
		return 0, fmt.Errorf("failed to advance the namespace ticket counter %s: %w", path, err)
	}
	return n, nil
}

func ticketPath(dir string, n uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%s%020d%s", ticketPrefix, n, ticketSuffix))
}

// release gives up the ticket and removes its file.
func (tk *ticket) release() error {
	rerr := os.Remove(tk.file.Name())
	if errors.Is(rerr, os.ErrNotExist) {
		rerr = nil
	}
	return errors.Join(rerr, tk.file.Close())
}

// isHead reports whether no live ticket is older than tk; the guard is
// held. Tickets of processes that died (their file lockable) are removed.
func (tk *ticket) isHead(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("failed to list the namespace slot dir %s: %w", dir, err)
	}
	for _, e := range entries {
		num, ok := strings.CutSuffix(strings.TrimPrefix(e.Name(), ticketPrefix), ticketSuffix)
		n, perr := strconv.ParseUint(num, 10, 64)
		if !ok || !strings.HasPrefix(e.Name(), ticketPrefix) || perr != nil || n >= tk.n {
			continue
		}
		f, err := lockFile(filepath.Join(dir, e.Name()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if err := errors.Join(os.Remove(f.Name()), f.Close()); err != nil {
			return false, fmt.Errorf("failed to drop the dead namespace ticket %s: %w", e.Name(), err)
		}
	}
	return true, nil
}
