package certutil

import (
	"testing"
)

func TestMustSerial_unique(t *testing.T) {
	a := mustSerial()
	b := mustSerial()
	if a.Cmp(b) == 0 {
		t.Fatal("CSPRNG serials must not collide")
	}
	if a.Sign() <= 0 || b.Sign() <= 0 {
		t.Fatal("serial must be positive")
	}
}
