package execution

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental"
)

const (
	bytesPerMB   = 1 << 20
	wasmPageSize = 64 << 10 // WebAssembly linear-memory page size
)

type memoryLimitKey struct{}

// WithMemoryLimitMB tags an execution ctx with the invoked function's own
// memory limit. ExecuteModule enforces it for that one invocation, on top of
// the runtime-wide cap. A value <= 0 leaves only the runtime-wide cap.
func WithMemoryLimitMB(ctx context.Context, mb int) context.Context {
	if mb <= 0 {
		return ctx
	}
	return context.WithValue(ctx, memoryLimitKey{}, mb)
}

func memoryLimitMBFrom(ctx context.Context) int {
	mb, _ := ctx.Value(memoryLimitKey{}).(int)
	return mb
}

// checkMinMemory refuses a module whose declared minimum memory already
// exceeds the function's limit: instantiation could never satisfy it.
func checkMinMemory(compiled wazero.CompiledModule, limitMB int) error {
	for name, def := range compiled.ExportedMemories() {
		if minPages := def.Min(); uint64(minPages)*wasmPageSize > uint64(limitMB)*bytesPerMB {
			return fmt.Errorf("module memory %q needs at least %d MB at start, exceeds the function's memory limit of %d MB",
				name, uint64(minPages)*wasmPageSize/bytesPerMB, limitMB)
		}
	}
	return nil
}

// cappedAllocator hands each memory of one instance a buffer that refuses to
// grow past limitBytes. memory.grow past the limit then returns -1 to the
// guest (wazero treats a nil Reallocate as a failed grow), which is how a
// guest sees any out-of-memory condition.
type cappedAllocator struct{ limitBytes uint64 }

func (a cappedAllocator) Allocate(capacity, _ uint64) experimental.LinearMemory {
	if capacity > a.limitBytes {
		capacity = a.limitBytes
	}
	return &cappedMemory{limitBytes: a.limitBytes, buf: make([]byte, 0, capacity)}
}

type cappedMemory struct {
	limitBytes uint64
	buf        []byte
}

// Reallocate implements experimental.LinearMemory. Memory never shrinks, so
// bytes between len and cap are still the zeros make() gave them.
func (m *cappedMemory) Reallocate(size uint64) []byte {
	if size > m.limitBytes {
		return nil
	}
	if size <= uint64(cap(m.buf)) {
		m.buf = m.buf[:size]
		return m.buf
	}
	newCap := uint64(cap(m.buf)) * 2
	if newCap < size {
		newCap = size
	}
	if newCap > m.limitBytes {
		newCap = m.limitBytes
	}
	grown := make([]byte, size, newCap)
	copy(grown, m.buf)
	m.buf = grown
	return m.buf
}

// Free implements experimental.LinearMemory.
func (m *cappedMemory) Free() { m.buf = nil }
