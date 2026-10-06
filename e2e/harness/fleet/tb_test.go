package fleet

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// failTB is a testing.TB whose failures are recorded instead of failing the
// real test, for asserting that a helper fails. Fatal ends the goroutine the
// helper runs on, as it does in a real test.
type failTB struct {
	testing.TB
	mu       sync.Mutex
	msgs     []string
	failed   bool
	cleanups []func()
}

func (f *failTB) Helper()                   {}
func (f *failTB) Name() string              { return "TestFake" }
func (f *failTB) Context() context.Context  { return context.Background() }
func (f *failTB) Cleanup(fn func())         { f.cleanups = append(f.cleanups, fn) }
func (f *failTB) Error(args ...any)         { f.fail(fmt.Sprint(args...)) }
func (f *failTB) Errorf(s string, a ...any) { f.fail(fmt.Sprintf(s, a...)) }
func (f *failTB) Fatal(args ...any)         { f.fail(fmt.Sprint(args...)); runtime.Goexit() }
func (f *failTB) Fatalf(s string, a ...any) {
	f.fail(fmt.Sprintf(s, a...))
	runtime.Goexit()
}
func (f *failTB) Failed() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.failed }

func (f *failTB) fail(msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = true
	f.msgs = append(f.msgs, msg)
}

// runFailTB runs fn as a test body, then its cleanups in reverse order.
func runFailTB(t *testing.T, fn func(tb testing.TB)) *failTB {
	tb := &failTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			for i := len(tb.cleanups) - 1; i >= 0; i-- {
				tb.cleanups[i]()
			}
		}()
		fn(tb)
	}()
	<-done
	return tb
}
