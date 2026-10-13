package setup

import (
	"context"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestEnsureVerifiedArchive_aCancelledContextTouchesNoMachine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The node has no SSH key: any attempt to reach it would fail with another error.
	err := ensureVerifiedArchive(ctx, inspector.Node{Host: "203.0.113.9"}, "/nonexistent/archive.tar.gz", "amd64", "abc", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled before the machine is asked anything", err)
	}
}
