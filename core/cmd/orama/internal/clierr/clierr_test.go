package clierr

import (
	"errors"
	"fmt"
	"testing"
)

// A script that can tell "you typed it wrong" from "the cluster refused" can
// act on the difference: the first is never worth retrying, the second may be.
// Every CLI failure used to be exit code 1.

func TestCodeOf_reads_the_code_a_constructor_set(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"usage", Usage("bad flag"), CodeUsage},
		{"auth", Auth("not logged in"), CodeAuth},
		{"not found", NotFound("no such node"), CodeNotFound},
		{"unavailable", Unavailable("gateway down"), CodeUnavailable},
		{"conflict", Conflict("would lose quorum"), CodeConflict},
		{"aborted", Aborted("cancelled"), CodeAborted},
		{"failure", Failure("broke"), CodeFailure},
	} {
		if got := CodeOf(tc.err); got != tc.want {
			t.Errorf("%s: CodeOf = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCodeOf_is_zero_for_success(t *testing.T) {
	if got := CodeOf(nil); got != CodeOK {
		t.Errorf("CodeOf(nil) = %d, want %d", got, CodeOK)
	}
}

// An unclassified error is still a failure, not a success.
func TestCodeOf_defaults_to_failure(t *testing.T) {
	if got := CodeOf(errors.New("something")); got != CodeFailure {
		t.Errorf("CodeOf = %d, want %d", got, CodeFailure)
	}
}

// A handler adds context with %w. Losing the classification there would put
// every wrapped error back at code 1.
func TestCodeOf_looks_through_wrapping(t *testing.T) {
	wrapped := fmt.Errorf("while starting the node: %w", Conflict("would lose quorum"))
	if got := CodeOf(wrapped); got != CodeConflict {
		t.Errorf("CodeOf = %d, want %d through a wrap", got, CodeConflict)
	}
}

func TestError_keeps_the_message(t *testing.T) {
	err := NotFound("node %s is not in the inventory", "1.2.3.4")
	if err.Error() != "node 1.2.3.4 is not in the inventory" {
		t.Errorf("Error() = %q", err.Error())
	}
}

// A caller may still want errors.Is against the underlying error.
func TestWrap_keeps_the_error_unwrappable(t *testing.T) {
	sentinel := errors.New("connection refused")
	wrapped := Wrap(CodeUnavailable, sentinel)

	if !errors.Is(wrapped, sentinel) {
		t.Error("the wrapped error must still match the original")
	}
	if CodeOf(wrapped) != CodeUnavailable {
		t.Errorf("CodeOf = %d, want %d", CodeOf(wrapped), CodeUnavailable)
	}
}

func TestWrap_of_nil_is_nil(t *testing.T) {
	if Wrap(CodeUsage, nil) != nil {
		t.Error("wrapping nil must stay nil, or every success becomes a failure")
	}
}

// %w inside a constructor must still unwrap.
func TestConstructors_support_wrapping(t *testing.T) {
	sentinel := errors.New("no such file")
	err := NotFound("could not read the key file: %w", sentinel)

	if !errors.Is(err, sentinel) {
		t.Error("the constructor must preserve %w")
	}
	if CodeOf(err) != CodeNotFound {
		t.Errorf("CodeOf = %d, want %d", CodeOf(err), CodeNotFound)
	}
}

// The codes have to stay distinct, or two different outcomes become
// indistinguishable to a caller.
func TestExitCodesAreDistinct(t *testing.T) {
	seen := map[int]string{}
	for name, code := range map[string]int{
		"OK": CodeOK, "Failure": CodeFailure, "Usage": CodeUsage,
		"Auth": CodeAuth, "NotFound": CodeNotFound, "Unavailable": CodeUnavailable,
		"Conflict": CodeConflict, "Aborted": CodeAborted,
	} {
		if other, clash := seen[code]; clash {
			t.Errorf("%s and %s share exit code %d", name, other, code)
		}
		seen[code] = name
	}
}

// Above 125 collides with what a shell reports for signals and for a command
// that could not be executed.
func TestExitCodesStayBelowTheShellsRange(t *testing.T) {
	for _, code := range []int{CodeFailure, CodeUsage, CodeAuth, CodeNotFound,
		CodeUnavailable, CodeConflict, CodeAborted} {
		if code < 1 || code > 125 {
			t.Errorf("exit code %d is outside the range a shell reports plainly", code)
		}
	}
}

func TestRequireRoot(t *testing.T) {
	err := RequireRoot("starting the node")
	// The test runner is not root in CI and is not expected to be.
	if err == nil {
		t.Skip("running as root")
	}
	if CodeOf(err) != CodeUsage {
		t.Errorf("CodeOf = %d, want %d: needing sudo is a usage mistake", CodeOf(err), CodeUsage)
	}
	if got := err.Error(); got == "" {
		t.Error("the message must say what needs root")
	}
}
