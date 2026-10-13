package orchardproc

import "time"

// timedMutex is a mutex that can also be taken with a time limit. It is a one-slot channel, so a
// wait with a limit is a select on a timer and a channel and not a loop of sleeps.
type timedMutex struct{ slot chan struct{} }

func newTimedMutex() *timedMutex { return &timedMutex{slot: make(chan struct{}, 1)} }

func (m *timedMutex) Lock()   { m.slot <- struct{}{} }
func (m *timedMutex) Unlock() { <-m.slot }

// TryLock takes the mutex if it is free.
func (m *timedMutex) TryLock() bool {
	select {
	case m.slot <- struct{}{}:
		return true
	default:
		return false
	}
}

// LockWithin takes the mutex, waiting at most d.
func (m *timedMutex) LockWithin(d time.Duration) bool {
	if m.TryLock() {
		return true
	}
	if d <= 0 {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case m.slot <- struct{}{}:
		return true
	case <-t.C:
		return false
	}
}
