package globalnode

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/globalnetns"
)

func funcPC(f any) uintptr { return reflect.ValueOf(f).Pointer() }

func TestDefaultLifecycle_waitsInsideTheNamespaceOnlyWhenColocated(t *testing.T) {
	plain := t.TempDir()
	if got := defaultLifecycle(plain, io.Discard).WaitChainRPC; funcPC(got) != funcPC(WaitChainRPC) {
		t.Error("a global-only machine does not probe the root namespace's loopback")
	}

	colocated := t.TempDir()
	if err := os.WriteFile(filepath.Join(colocated, globalnetns.UnitName), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := defaultLifecycle(colocated, io.Discard)
	if funcPC(l.WaitChainRPC) != funcPC(WaitChainRPCInNamespace) {
		t.Error("a co-located machine probes the root loopback, where the chain does not listen")
	}
	if l.UnitDir != colocated {
		t.Errorf("UnitDir = %q, want %q", l.UnitDir, colocated)
	}
}
