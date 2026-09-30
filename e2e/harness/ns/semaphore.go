package ns

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The fleet-wide cap on live namespaces. Every namespace takes a port block
// on three nodes and a node has twenty blocks
// (core/pkg/namespace/types.go MaxNamespacesPerNode, BlueprintTenant N=3),
// so three core nodes host twenty namespaces; headroom is kept for
// namespaces created outside ns.New (the capacity tests, the bootstrap).
const (
	// EnvMaxLive overrides the cap (a positive integer).
	EnvMaxLive = "E2E_MAX_LIVE_NAMESPACES"
	// blocksPerNode is MaxNamespacesPerNode.
	blocksPerNode = 20
	// nodesPerNamespace is the tenant size on a fleet of three or more.
	nodesPerNamespace = 3
	// liveHeadroom is left for namespaces ns.New does not create.
	liveHeadroom = 4
	// slotDirName holds one lock file per slot, in the run's work dir.
	slotDirName  = "ns-slots"
	slotFileMode = 0o600
	slotDirMode  = 0o700
)

// slotPoll is how often a waiting Reserve looks for free slots (a variable
// so tests can shorten it).
var slotPoll = 2 * time.Second

// DefaultMaxLive is the cap for a fleet of coreNodes nodes (16 for three).
func DefaultMaxLive(coreNodes int) int {
	return max(1, coreNodes*blocksPerNode/nodesPerNamespace-liveHeadroom)
}

// StagenetMaxLive is the default cap on the stagenet target. Its three nodes
// are shared, small VPSs that also serve the owner's own namespaces, and a
// namespace that nobody can delete (its owner was a run's throwaway wallet)
// keeps its port block: sixteen at once starved them until provisioning
// rolled back and then answered "insufficient nodes available" (2026-09-30).
const StagenetMaxLive = 4

// MaxLiveFromEnv is EnvMaxLive through lookup, or DefaultMaxLive(coreNodes).
func MaxLiveFromEnv(lookup func(string) (string, bool), coreNodes int) (int, error) {
	return maxLive(lookup, DefaultMaxLive(coreNodes))
}

// MaxLiveForTarget is EnvMaxLive through lookup, or the target's default:
// StagenetMaxLive on stagenet, DefaultMaxLive for a fleet of coreNodes.
func MaxLiveForTarget(lookup func(string) (string, bool), stagenet bool, coreNodes int) (int, error) {
	if stagenet {
		return maxLive(lookup, StagenetMaxLive)
	}
	return MaxLiveFromEnv(lookup, coreNodes)
}

func maxLive(lookup func(string) (string, bool), fallback int) (int, error) {
	v, ok := lookup(EnvMaxLive)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s=%q must be a positive integer", EnvMaxLive, v)
	}
	return n, nil
}

// Slots are live-namespace slots held by this process: one flock'd file per
// slot in <work dir>/ns-slots. A process that dies releases its slots with
// its file descriptors, so a crashed package never leaks the fleet's room.
type Slots struct {
	files []*os.File
}

// Reserve takes count of the capacity slots of the run whose work dir is
// workDir, waiting (polling every slotPoll) until that many are free at
// once: a test never holds part of its slots while waiting for the rest.
// Waiters are served in arrival order across every process of the run (see
// queue.go), so one asking for several slots is never starved. It returns
// ctx's error when ctx ends first.
func Reserve(ctx context.Context, workDir string, count, capacity int) (*Slots, error) {
	if count < 1 || count > capacity {
		return nil, fmt.Errorf("a test may hold 1 to %d namespaces at once (the fleet-wide cap, %s), asked for %d", capacity, EnvMaxLive, count)
	}
	dir := filepath.Join(workDir, slotDirName)
	if err := os.MkdirAll(dir, slotDirMode); err != nil {
		return nil, fmt.Errorf("failed to create the namespace slot dir %s: %w", dir, err)
	}
	tk, err := takeTicket(dir)
	if err != nil {
		return nil, err
	}
	s, err := waitTurn(ctx, dir, tk, count, capacity)
	return s, errors.Join(err, tk.release())
}

// waitTurn polls until tk is the oldest live ticket and count slots are free.
func waitTurn(ctx context.Context, dir string, tk *ticket, count, capacity int) (*Slots, error) {
	ticker := time.NewTicker(slotPoll)
	defer ticker.Stop()
	for {
		s, err := tryReserveInTurn(dir, tk, count, capacity)
		if err != nil || s != nil {
			return s, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for %d of %d live-namespace slots: %w", count, capacity, ctx.Err())
		case <-ticker.C:
		}
	}
}

// tryReserveInTurn takes count free slots when tk is at the head of the
// queue, or nothing.
func tryReserveInTurn(dir string, tk *ticket, count, capacity int) (*Slots, error) {
	guard, err := lockFile(filepath.Join(dir, guardName), syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	head, err := tk.isHead(dir)
	if err != nil || !head {
		return nil, err
	}
	return takeSlots(dir, count, capacity)
}

// takeSlots takes count free slots or none; the guard is held.
func takeSlots(dir string, count, capacity int) (*Slots, error) {
	s := &Slots{}
	for i := 0; i < capacity && len(s.files) < count; i++ {
		f, err := lockFile(filepath.Join(dir, fmt.Sprintf("slot-%03d.lock", i)), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			continue
		}
		if err != nil {
			return nil, errors.Join(err, s.Release())
		}
		s.files = append(s.files, f)
	}
	if len(s.files) < count {
		return nil, s.Release()
	}
	return s, nil
}

// lockFile opens path and flocks it with how; the lock lives as long as the
// returned file is open. A lock that would block (LOCK_NB) is EWOULDBLOCK.
func lockFile(path string, how int) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, slotFileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, syscall.EWOULDBLOCK
		}
		return nil, fmt.Errorf("failed to lock %s: %w", path, err)
	}
	return f, nil
}

// Release gives the slots back. Releasing twice is not an error.
func (s *Slots) Release() error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, f := range s.files {
		if err := f.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to release namespace slot %s: %w", f.Name(), err))
		}
	}
	s.files = nil
	return errors.Join(errs...)
}

// Held is how many slots s holds.
func (s *Slots) Held() int { return len(s.files) }
