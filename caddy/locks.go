package orama

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// lockTable is the locks this process holds, by name.
type lockTable struct {
	mu   sync.Mutex
	held map[string]*heldLock
}

type heldLock struct {
	holder string
	stop   context.CancelFunc
}

// Lock blocks until this process holds name, then keeps its lease renewed
// until Unlock.
func (s *Storage) Lock(ctx context.Context, name string) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("orama storage lock %s: draw a holder id: %w", name, err)
	}
	holder := hex.EncodeToString(raw)
	for {
		resp, err := s.call(ctx, storeRequest{Op: "lock", Key: name, Holder: holder, LeaseMS: lockLease.Milliseconds()})
		if err != nil {
			return err
		}
		if resp.Acquired {
			s.hold(name, holder)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("orama storage lock %s: %w", name, ctx.Err())
		case <-time.After(lockPoll):
		}
	}
}

// hold records the lock and renews its lease every lockLease/3.
func (s *Storage) hold(name, holder string) {
	keepCtx, stop := context.WithCancel(context.Background())
	s.locks.mu.Lock()
	s.locks.held[name] = &heldLock{holder: holder, stop: stop}
	s.locks.mu.Unlock()
	go func() {
		t := time.NewTicker(lockLease / 3)
		defer t.Stop()
		for {
			select {
			case <-keepCtx.Done():
				return
			case <-t.C:
				callCtx, cancel := context.WithTimeout(keepCtx, renewTimeout)
				_, err := s.call(callCtx, storeRequest{Op: "renew", Key: name, Holder: holder, LeaseMS: lockLease.Milliseconds()})
				cancel()
				switch {
				case err == nil || keepCtx.Err() != nil:
				case errors.Is(err, errLockNotHeld):
					s.logger.Error("lost a TLS store lock: its lease ran out and another node took it", zap.String("lock", name))
					return
				default:
					s.logger.Error("could not renew a TLS store lock; another node may take it", zap.String("lock", name), zap.Error(err))
				}
			}
		}
	}()
}

// Unlock releases name.
func (s *Storage) Unlock(ctx context.Context, name string) error {
	s.locks.mu.Lock()
	held, ok := s.locks.held[name]
	delete(s.locks.held, name)
	s.locks.mu.Unlock()
	if !ok {
		return fmt.Errorf("orama storage unlock %s: %w", name, errLockNotHeld)
	}
	held.stop()
	_, err := s.call(ctx, storeRequest{Op: "unlock", Key: name, Holder: held.holder})
	return err
}

// RenewLockLease extends the lease on a lock this process holds. A lease
// longer than the store allows is cut to it; the holder keeps renewing either
// way.
func (s *Storage) RenewLockLease(ctx context.Context, name string, lease time.Duration) error {
	s.locks.mu.Lock()
	held, ok := s.locks.held[name]
	s.locks.mu.Unlock()
	if !ok {
		return fmt.Errorf("orama storage renew %s: %w", name, errLockNotHeld)
	}
	lease = min(max(lease, lockLease), maxLease)
	_, err := s.call(ctx, storeRequest{Op: "renew", Key: name, Holder: held.holder, LeaseMS: lease.Milliseconds()})
	return err
}
