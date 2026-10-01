package functions

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Bug: `orama function get ../../v1/namespace/list` (and versions, invoke, logs,
// enable, ...) reached the credential lookup, and a request path, before the
// name was looked at. A name that is not a function name is a usage error.
func TestFunctionNameCommands_refuseABadNameBeforeAnyRequest(t *testing.T) {
	commands := map[string]struct {
		cmd  *cobra.Command
		rest []string
	}{
		"get": {GetCmd, nil}, "delete": {DeleteCmd, nil}, "versions": {VersionsCmd, nil}, "logs": {LogsCmd, nil},
		"invoke": {InvokeCmd, nil}, "enable": {EnableCmd, nil}, "disable": {DisableCmd, nil},
		"triggers add": {TriggersAddCmd, nil}, "triggers list": {TriggersListCmd, nil},
		"triggers delete": {TriggersDeleteCmd, []string{"trigger-1"}},
	}
	for name, c := range commands {
		for _, bad := range []string{"../../v1/namespace/list", "a/b", "9starts-with-digit", "has space", "tab\tname", "rtl\u202e", "-lead", ""} {
			if err := c.cmd.Args(c.cmd, append([]string{bad}, c.rest...)); err == nil || !strings.Contains(err.Error(), "invalid function name") {
				t.Errorf("%s %q: %v, want an invalid function name refusal", name, bad, err)
			}
		}
		for _, good := range []string{"a", "my-function", "hello_world", "F1"} {
			if err := c.cmd.Args(c.cmd, append([]string{good}, c.rest...)); err != nil {
				t.Errorf("%s %q refused: %v", name, good, err)
			}
		}
		if err := c.cmd.Args(c.cmd, nil); err == nil {
			t.Errorf("%s accepted no name", name)
		}
		if err := c.cmd.Args(c.cmd, append([]string{"ok", "surplus"}, c.rest...)); err == nil {
			t.Errorf("%s accepted a surplus argument", name)
		}
	}
}
