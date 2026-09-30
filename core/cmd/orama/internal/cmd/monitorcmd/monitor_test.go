package monitorcmd

import "testing"

// A positional argument can only be a view that does not exist. It was
// ignored and the live view opened (stagenet e2e, 2026-09-30); it is refused,
// and the CLI's usage classification turns the refusal into exit 2.
func TestMonitor_refusesAPositionalArgument(t *testing.T) {
	if Cmd.Args == nil {
		t.Fatal("monitor accepts any positional argument")
	}
	if err := Cmd.Args(Cmd, []string{"e2e-no-such-view"}); err == nil {
		t.Error("a view that does not exist was accepted")
	}
	if err := Cmd.Args(Cmd, nil); err != nil {
		t.Errorf("monitor with no argument was refused: %v", err)
	}
}
