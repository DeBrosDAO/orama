package tlsstatus

import (
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"testing"
)

// exitWith is a real *exec.ExitError of the given status, as journalctl's would be.
func exitWith(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("sh did not exit %d: %v", code, err)
	}
	return err
}

func TestJournalArgs_grepBeforeTheCount(t *testing.T) {
	got := journalArgs(CaddyUnit, journalLines)
	grep, count := slices.Index(got, "--grep"), slices.Index(got, "-n")
	if grep < 0 || count < 0 || got[grep+1] != certificateLoggerPattern || got[count+1] != strconv.Itoa(journalLines) {
		t.Fatalf("journalctl %v: want --grep %s and -n %d", got, certificateLoggerPattern, journalLines)
	}
	if !slices.Contains(got, "--reverse") {
		t.Fatalf("journalctl %v: without --reverse, systemd 249 and 252 count the unit's entries before they filter", got)
	}
	if !slices.Contains(got, "cat") || !slices.Contains(got, CaddyUnit) {
		t.Fatalf("journalctl %v: want the unit's messages alone", got)
	}
}

func TestJournalResult(t *testing.T) {
	cases := []struct {
		name        string
		out, stderr string
		err         error
		want        string
		wantErr     bool
	}{
		{name: "lines", out: rateLimitedLine + "\n", want: rateLimitedLine + "\n"},
		{name: "no match, nothing printed", err: exitWith(t, 1)},
		{name: "no match, the no-entries line", out: "-- No entries --\n", err: exitWith(t, 1)},
		{name: "exit 1 with an error on stderr", stderr: "Failed to open journal: permission denied", err: exitWith(t, 1), wantErr: true},
		{name: "exit 1 with other output", out: "something else", err: exitWith(t, 1), wantErr: true},
		{name: "another exit status", err: exitWith(t, 2), wantErr: true},
		{name: "journalctl not found", err: exec.ErrNotFound, wantErr: true},
	}
	for _, c := range cases {
		got, err := journalResult(CaddyUnit, []byte(c.out), []byte(c.stderr), c.err)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s: got %q, %v; want %q, error %v", c.name, got, err, c.want, c.wantErr)
		}
	}
}

// journalctl --reverse answers newest first; the parser reads in the order the lines were written,
// so a failure newer than a success is the state and a success newer than a failure clears it.
func TestOldestFirst_aFailureAfterASuccessStands(t *testing.T) {
	newestFirst := rateLimitedLine + "\n" + obtainedLine + "\n"
	got := lastObtainErrors(oldestFirst(newestFirst), "")
	if _, ok := got["stagenet.example.org"]; !ok {
		t.Fatalf("the failure written after the success was lost: %+v", got)
	}
	cleared := lastObtainErrors(oldestFirst(obtainedLine+"\n"+rateLimitedLine+"\n"), "")
	if len(cleared) != 0 {
		t.Fatalf("a success written after the failure did not clear it: %+v", cleared)
	}
}

func TestOldestFirst_edges(t *testing.T) {
	for in, want := range map[string]string{"": "", "a\n": "a\n", "b\na\n": "a\nb\n", "b\na": "a\nb\n"} {
		if got := oldestFirst(in); got != want {
			t.Errorf("oldestFirst(%q) = %q, want %q", in, got, want)
		}
	}
}
