//go:build !cgo || !orchardffi

package orchard

import (
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

func TestTree_unlinkedFailsClosed(t *testing.T) {
	if _, _, err := NewTree().Append(nil, nil); !errors.Is(err, verify.ErrVerifierNotLinked) {
		t.Fatalf("Append: %v", err)
	}
	if _, err := NewTree().EmptyRoot(); !errors.Is(err, verify.ErrVerifierNotLinked) {
		t.Fatalf("EmptyRoot: %v", err)
	}
}
