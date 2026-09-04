package node

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// The help text is built from the alias table rather than repeating it, so an
// alias cannot be added without being documented. This fails if someone
// replaces the call with a literal list.
func TestLogsCommand_helpNamesEveryAlias(t *testing.T) {
	for _, alias := range utils.ServiceAliases() {
		if !strings.Contains(logsCmd.Long, alias) {
			t.Errorf("`orama node logs --help` does not mention the %q alias", alias)
		}
	}
}

// A tenant service is the case the command exists for, and the one that used
// to fail, so the help has to show its shape.
func TestLogsCommand_helpShowsATemplateInstance(t *testing.T) {
	if !strings.Contains(logsCmd.Long, "orama-namespace-olric@anchat") {
		t.Error("the help does not show how to name a tenant service")
	}
}

func TestLogsCommand_rejectsANonPositiveLineCount(t *testing.T) {
	previous := logsLines
	t.Cleanup(func() { logsLines = previous })

	for _, lines := range []int{0, -1} {
		logsLines = lines
		err := logsCmd.RunE(logsCmd, []string{"node"})
		if err == nil {
			t.Fatalf("--lines %d was accepted", lines)
		}
		if !strings.Contains(err.Error(), "--lines") {
			t.Errorf("--lines %d: error %q does not name the flag", lines, err)
		}
	}
}

// --since is what the documented diagnostics use: they grep for a line that
// fires once a minute, which a fixed line count can miss entirely on a busy
// node. The help has to show it.
func TestLogsCommand_helpShowsTheSinceWindow(t *testing.T) {
	if !strings.Contains(logsCmd.Long, "--since") {
		t.Error("the help does not mention --since")
	}
}

func TestLogsCommand_sinceFlagExists(t *testing.T) {
	flag := logsCmd.Flags().Lookup("since")
	if flag == nil {
		t.Fatal("orama node logs has no --since flag")
	}
	if flag.DefValue != "" {
		t.Errorf("--since defaults to %q; it has to be off unless asked for", flag.DefValue)
	}
}
