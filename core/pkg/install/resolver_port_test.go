package install

import (
	"errors"
	"testing"
)

// A nameserver whose stub listener could not be disabled used to install with
// a warning and then fail to bind :53; it fails the install instead.
func TestFreeResolverPort(t *testing.T) {
	failing := func() error { return errors.New("resolved.conf is read-only") }
	if err := freeResolverPort(true, failing); err == nil {
		t.Fatal("a nameserver kept :53 with the stub and the install carried on")
	}
	called := false
	if err := freeResolverPort(false, func() error { called = true; return nil }); err != nil || called {
		t.Errorf("a regular node touched the stub listener: %v, %v", err, called)
	}
	if err := freeResolverPort(true, func() error { return nil }); err != nil {
		t.Errorf("success reported as failure: %v", err)
	}
}
