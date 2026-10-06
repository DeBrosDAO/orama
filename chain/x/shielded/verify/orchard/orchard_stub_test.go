//go:build !cgo || !orchardffi

package orchard

import (
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// A build without the Rust library must refuse every bundle, never accept one.
func TestNew_withoutRustLibraryFailsClosed(t *testing.T) {
	if Linked {
		t.Fatal("stub build reports Linked")
	}
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	v := mustNew(t, chainID)
	if err := v.Verify(bundle, nil); !errors.Is(err, verify.ErrVerifierNotLinked) {
		t.Fatalf("got %v, want ErrVerifierNotLinked", err)
	}
	if err := verify.Check(bundle, nil, v, v); !errors.Is(err, verify.ErrVerifierNotLinked) {
		t.Fatalf("Check with two stubs: %v", err)
	}
}

func TestWarm_withoutRustLibraryReportsNotLinked(t *testing.T) {
	if err := Warm(); !errors.Is(err, verify.ErrVerifierNotLinked) {
		t.Fatalf("Warm = %v, want ErrVerifierNotLinked", err)
	}
}
