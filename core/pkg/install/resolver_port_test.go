package install

import (
	"errors"
	"testing"
)

func ok() error { return nil }

// A nameserver whose stub listener could not be disabled used to install with
// a warning and then fail to bind :53; it fails the install instead.
func TestFreeResolverPort(t *testing.T) {
	failing := func() error { return errors.New("resolved.conf is read-only") }
	if err := freeResolverPort(true, failing, ok); err == nil {
		t.Fatal("a nameserver kept :53 with the stub and the install carried on")
	}
	called := false
	if err := freeResolverPort(false, func() error { called = true; return nil }, ok); err != nil || called {
		t.Errorf("a regular node touched the stub listener: %v, %v", err, called)
	}
	if err := freeResolverPort(true, ok, ok); err != nil {
		t.Errorf("success reported as failure: %v", err)
	}
}

// LLMNR and mDNS are turned off on every node that runs resolved, not only on
// a nameserver, and a failure fails the install.
func TestFreeResolverPort_multicastOffOnEveryNode(t *testing.T) {
	for _, ns := range []bool{true, false} {
		called := false
		if err := freeResolverPort(ns, ok, func() error { called = true; return nil }); err != nil || !called {
			t.Errorf("isNameserver=%v: multicast not turned off: err=%v called=%v", ns, err, called)
		}
		if err := freeResolverPort(ns, ok, func() error { return errors.New("boom") }); err == nil {
			t.Errorf("isNameserver=%v: multicast failure swallowed", err)
		}
	}
}
