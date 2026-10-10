package cache

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

// awaitWaiters blocks until n callers are waiting on the compile of cid.
func awaitWaiters(t *testing.T, c *ModuleCache, cid string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		call := c.inflight[cid]
		got := 0
		if call != nil {
			got = call.waiters
		}
		c.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers were waiting on %s", got, n, cid)
		}
		runtime.Gosched()
	}
}

// minimalWASM is the empty module: magic number and version.
var minimalWASM = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func compileEmpty(t *testing.T) wazero.CompiledModule {
	t.Helper()
	rt := wazero.NewRuntime(context.Background())
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	m, err := rt.CompileModule(context.Background(), minimalWASM)
	if err != nil {
		t.Fatalf("failed to compile the empty module: %v", err)
	}
	return m
}

func TestGetOrCompute_concurrentColdCallersShareOneCompile(t *testing.T) {
	c := NewModuleCache(4, zap.NewNop())
	mod := compileEmpty(t)
	var computes atomic.Int32
	release := make(chan struct{})

	const callers = 20
	var wg sync.WaitGroup
	results := make([]wazero.CompiledModule, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m, err := c.GetOrCompute(context.Background(), "cid", func() (wazero.CompiledModule, error) {
				computes.Add(1)
				<-release
				return mod, nil
			})
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
			}
			results[i] = m
		}(i)
	}
	awaitWaiters(t, c, "cid", callers-1)
	close(release)
	wg.Wait()

	if n := computes.Load(); n != 1 {
		t.Errorf("%d concurrent cold callers compiled %d times, want 1", callers, n)
	}
	for i, m := range results {
		if m != mod {
			t.Errorf("caller %d got a different module", i)
		}
	}
}

func TestGetOrCompute_failureIsSharedAndNotCached(t *testing.T) {
	c := NewModuleCache(4, zap.NewNop())
	boom := errors.New("fetch failed")
	if _, err := c.GetOrCompute(context.Background(), "cid", func() (wazero.CompiledModule, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	mod := compileEmpty(t)
	got, err := c.GetOrCompute(context.Background(), "cid", func() (wazero.CompiledModule, error) { return mod, nil })
	if err != nil || got != mod {
		t.Errorf("a failed compile was remembered: module %v, err %v", got, err)
	}
}

func TestGetOrCompute_waiterRecomputesWhenLeaderWasCancelled(t *testing.T) {
	c := NewModuleCache(4, zap.NewNop())
	mod := compileEmpty(t)
	leaderIn := make(chan struct{})
	leaderGo := make(chan struct{})
	go func() {
		_, _ = c.GetOrCompute(context.Background(), "cid", func() (wazero.CompiledModule, error) {
			close(leaderIn)
			<-leaderGo
			return nil, context.Canceled
		})
	}()
	<-leaderIn
	done := make(chan error, 1)
	go func() {
		m, err := c.GetOrCompute(context.Background(), "cid", func() (wazero.CompiledModule, error) { return mod, nil })
		if err == nil && m != mod {
			err = errors.New("wrong module")
		}
		done <- err
	}()
	awaitWaiters(t, c, "cid", 1)
	close(leaderGo)
	if err := <-done; err != nil {
		t.Errorf("waiter after a cancelled leader: %v", err)
	}
}

func TestGetOrCompute_waiterHonoursItsOwnContext(t *testing.T) {
	c := NewModuleCache(4, zap.NewNop())
	in := make(chan struct{})
	block := make(chan struct{})
	defer close(block)
	go func() {
		_, _ = c.GetOrCompute(context.Background(), "cid", func() (wazero.CompiledModule, error) {
			close(in)
			<-block
			return nil, errors.New("never")
		})
	}()
	<-in
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.GetOrCompute(ctx, "cid", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
}

func TestGetOrCompute_panickingComputeReleasesWaitersAndFreesTheCID(t *testing.T) {
	c := NewModuleCache(4, zap.NewNop())
	in := make(chan struct{})
	boom := make(chan struct{})
	leaderPanic := make(chan any, 1)
	go func() {
		defer func() { leaderPanic <- recover() }()
		_, _ = c.GetOrCompute(context.Background(), "cid", func() (wazero.CompiledModule, error) {
			close(in)
			<-boom
			panic("compiler blew up")
		})
	}()
	<-in
	waiter := make(chan error, 1)
	go func() {
		_, err := c.GetOrCompute(context.Background(), "cid", nil)
		waiter <- err
	}()
	awaitWaiters(t, c, "cid", 1)
	close(boom)

	if err := <-waiter; !errors.Is(err, errCompilePanicked) {
		t.Errorf("waiter err = %v, want errCompilePanicked", err)
	}
	if r := <-leaderPanic; r != "compiler blew up" {
		t.Errorf("the leader's panic = %v, want it to continue", r)
	}

	mod := compileEmpty(t)
	got, err := c.GetOrCompute(context.Background(), "cid", func() (wazero.CompiledModule, error) { return mod, nil })
	if err != nil || got != mod {
		t.Errorf("a compile after the panic: module %v, err %v", got, err)
	}
}
