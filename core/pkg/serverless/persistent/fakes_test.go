package persistent

import (
	"context"
	"sync/atomic"
	"time"
)

// entryProbe records how many goroutines are inside the "module" at once.
//
// A real wazero instance does not detect concurrent entry — it corrupts its
// guest stack and the Go runtime dies with a fatal error that recover() cannot
// catch, taking the whole gateway process down. wazero's own docs say Call
// "should not be called multiple times until the previous Call returns". So the
// invariant is asserted here instead.
type entryProbe struct {
	inside    atomic.Int32
	maxInside atomic.Int32
	calls     atomic.Int32
	hold      time.Duration
}

func (p *entryProbe) enter() {
	n := p.inside.Add(1)
	for {
		cur := p.maxInside.Load()
		if n <= cur || p.maxInside.CompareAndSwap(cur, n) {
			break
		}
	}
	p.calls.Add(1)
	if p.hold > 0 {
		time.Sleep(p.hold)
	}
	p.inside.Add(-1)
}

// fakeFunction is an api.Function whose Call routes through the probe.
type fakeFunction struct {
	probe  *entryProbe
	result uint64
}

func (f *fakeFunction) Call(ctx context.Context, params ...uint64) ([]uint64, error) {
	f.probe.enter()
	return []uint64{f.result}, nil
}

// fakeMemory satisfies guestMemory.
type fakeMemory struct{ buf []byte }

func (m *fakeMemory) Write(offset uint32, v []byte) bool {
	need := int(offset) + len(v)
	if need > len(m.buf) {
		grown := make([]byte, need)
		copy(grown, m.buf)
		m.buf = grown
	}
	copy(m.buf[offset:], v)
	return true
}

// fakeModule satisfies guestModule. Close routes through the probe so a
// teardown racing an in-flight frame is caught exactly as a real wazero
// module.Close would race guest execution.
type fakeModule struct {
	probe  *entryProbe
	closed atomic.Bool
}

func (m *fakeModule) Close(context.Context) error {
	m.probe.enter()
	m.closed.Store(true)
	return nil
}
