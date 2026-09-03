package rqlite

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactDSN_StripsPassword(t *testing.T) {
	in := "http://orama:s3cret-pass@127.0.0.1:10100"
	got := RedactDSN(in)
	if strings.Contains(got, "s3cret-pass") {
		t.Fatalf("password still present: %s", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Fatalf("expected REDACTED, got %s", got)
	}
	if !strings.Contains(got, "orama") {
		t.Fatalf("username should remain: %s", got)
	}
}

func TestRedactError_MalformedDSN(t *testing.T) {
	dsn := "http://orama:s3cret-pass@127.0.0.1:10100"
	err := errors.New(`parse "http://orama:s3cret-pass@127.0.0.1:10100": invalid port`)
	got := RedactError(err, dsn)
	if strings.Contains(got, "s3cret-pass") {
		t.Fatalf("password leaked in error: %s", got)
	}
}
