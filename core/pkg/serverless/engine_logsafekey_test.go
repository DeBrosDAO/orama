package serverless

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLogSafeKey_shortKeyUnchanged(t *testing.T) {
	if got := logSafeKey([]byte("price:sol")); got != "price:sol" {
		t.Fatalf("logSafeKey = %q, want the key unchanged", got)
	}
}

func TestLogSafeKey_longKeyTruncatedWithLength(t *testing.T) {
	key := []byte(strings.Repeat("k", 10_000))
	got := logSafeKey(key)
	if len(got) > maxLoggedKeyBytes+32 {
		t.Fatalf("logSafeKey kept %d bytes of a 10000-byte key", len(got))
	}
	if !strings.Contains(got, "(10000 bytes)") {
		t.Fatalf("logSafeKey = %q, want the original length recorded", got)
	}
}

func TestLogSafeKey_doesNotSplitARune(t *testing.T) {
	// 63 ASCII bytes then a 3-byte rune: the cut at 64 lands inside it.
	key := []byte(strings.Repeat("a", maxLoggedKeyBytes-1) + "€€€")
	if got := logSafeKey(key); !utf8.ValidString(got) {
		t.Fatalf("logSafeKey produced invalid UTF-8: %q", got)
	}
}
