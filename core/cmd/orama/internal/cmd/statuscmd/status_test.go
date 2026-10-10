package statuscmd

import (
	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"strings"
	"testing"
)

// No --operator means no account half; a value that is not an orama address is refused before it
// becomes part of a query.
func TestOperatorReader_emptyAndMalformedAddresses(t *testing.T) {
	read, err := operatorReader("")
	if err != nil || read != nil {
		t.Fatalf("no operator: %v %v", read != nil, err)
	}
	for _, bad := range []string{"cosmos1abc", "orama1abc/../x", "orama1 abc", "orama1abc?x=1"} {
		if _, err := operatorReader(bad); err == nil || !strings.Contains(err.Error(), "orama address") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

// Every one-shot view of the old monitor command is a subcommand of status.
func TestCmd_mountsEveryView(t *testing.T) {
	for _, want := range []string{"cluster", "node", "service", "mesh", "dns", "namespaces", "alerts", "traffic", "chain", "report"} {
		found := false
		for _, c := range Cmd.Commands() {
			found = found || c.Name() == want
		}
		if !found {
			t.Errorf("orama status has no %q subcommand", want)
		}
	}
}

// A positional argument can only be a view name that does not exist; it is refused, not ignored.
func TestCmd_refusesAPositionalArgument(t *testing.T) {
	if err := Cmd.Args(Cmd, []string{"clustr"}); err == nil {
		t.Fatal("orama status accepted a stray argument")
	}
}

const recordedOperator = "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"

func recordEnvironment(t *testing.T, name string, withOperator bool) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if err := cli.AddEnvironment(name, "https://cluster.example.org", "test"); err != nil {
		t.Fatal(err)
	}
	if withOperator {
		if err := cli.RecordOperator(name, recordedOperator); err != nil {
			t.Fatal(err)
		}
	}
}

// `orama setup` records the operator on the environment; status shows it without --operator.
func TestOperatorToShow_defaultsToTheRecordedOperator(t *testing.T) {
	recordEnvironment(t, "mine", true)
	if got := operatorToShow("", "mine"); got != recordedOperator {
		t.Fatalf("operatorToShow = %q, want the recorded %q", got, recordedOperator)
	}
}

func TestOperatorToShow_theFlagWins(t *testing.T) {
	recordEnvironment(t, "mine", true)
	const other = "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	if got := operatorToShow(other, "mine"); got != other {
		t.Fatalf("operatorToShow = %q, want the flag's %q", got, other)
	}
}

func TestOperatorToShow_nothingRecorded(t *testing.T) {
	recordEnvironment(t, "mine", false)
	if got := operatorToShow("", "mine"); got != "" {
		t.Fatalf("operatorToShow = %q, want none", got)
	}
	if got := operatorToShow("", "unknown"); got != "" {
		t.Fatalf("an unknown environment gave %q", got)
	}
}
