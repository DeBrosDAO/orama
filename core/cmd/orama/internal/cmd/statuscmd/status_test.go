package statuscmd

import (
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
