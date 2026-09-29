//go:build linux

package globalnetns

import (
	"context"
	"fmt"
	"net"
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

// DialContext dials addr from inside the namespace at path. addr must be an
// IP literal and port: a host name would be resolved by helper goroutines
// outside the namespace.
func DialContext(path string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var conn net.Conn
		err := InNamespace(path, func() error {
			var d net.Dialer
			c, err := d.DialContext(ctx, network, addr)
			conn = c
			return err
		})
		return conn, err
	}
}
