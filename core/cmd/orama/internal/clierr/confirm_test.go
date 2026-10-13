package clierr

import (
	"strings"
	"testing"
)

func TestConfirm_acceptedAnswer(t *testing.T) {
	for _, in := range []string{"yes\n", "  yes \n", "yes"} {
		if err := Confirm(strings.NewReader(in), "yes"); err != nil {
			t.Errorf("Confirm(%q) = %v, want nil", in, err)
		}
	}
	if err := Confirm(strings.NewReader("y\n"), "y", "yes"); err != nil {
		t.Errorf("Confirm(y) with y/yes accepted = %v, want nil", err)
	}
}

func TestConfirm_declinedIsAborted(t *testing.T) {
	for _, in := range []string{"no\n", "y\n", "yess\n", "YES\n", "Yes\n", "\n"} {
		err := Confirm(strings.NewReader(in), "yes")
		if err == nil {
			t.Fatalf("Confirm(%q) accepted, want declined", in)
		}
		if got := CodeOf(err); got != CodeAborted {
			t.Errorf("Confirm(%q) exit code = %d, want %d (CodeAborted)", in, got, CodeAborted)
		}
	}
}

func TestConfirm_endOfInputIsAborted(t *testing.T) {
	err := Confirm(strings.NewReader(""), "yes")
	if CodeOf(err) != CodeAborted {
		t.Fatalf("Confirm on empty input = %v, want CodeAborted", err)
	}
	if err := Confirm(strings.NewReader("anything\n")); CodeOf(err) != CodeAborted {
		t.Fatalf("Confirm with nothing accepted = %v, want CodeAborted", err)
	}
}
