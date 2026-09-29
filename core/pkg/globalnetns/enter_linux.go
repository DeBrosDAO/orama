//go:build linux

package globalnetns

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// InNamespace runs fn on a thread that has joined the network namespace at
// path. A socket fn creates lives in that namespace for good, so a listener
// or connection made inside fn can be used afterwards from any goroutine.
//
// The goroutine is never unlocked from its thread: when it returns, the
// runtime discards the thread, so no other goroutine can run on it in the
// wrong namespace. fn must not start goroutines that open sockets; they would
// run on other threads, in the root namespace.
func InNamespace(path string, fn func() error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		f, err := os.Open(path)
		if err != nil {
			done <- fmt.Errorf("open network namespace %s: %w", path, err)
			return
		}
		defer f.Close()
		if err := unix.Setns(int(f.Fd()), unix.CLONE_NEWNET); err != nil {
			done <- fmt.Errorf("join network namespace %s: %w", path, err)
			return
		}
		done <- fn()
	}()
	return <-done
}
