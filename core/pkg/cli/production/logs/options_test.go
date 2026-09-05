package logs

import (
	"strings"
	"testing"
)

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestOptions_sinceReplacesTheLineCount(t *testing.T) {
	args := journalctlArgs("orama-node", Options{Since: "-30min", Lines: DefaultLines})

	if !has(args, "--since=-30min") {
		t.Errorf("--since did not reach journalctl: %v", args)
	}
	if has(args, "-n") {
		t.Errorf("--since was bounded by a line count as well: %v — a window and a "+
			"count together silently truncate the window", args)
	}
}

func TestOptions_anExplicitLineCountSurvivesSince(t *testing.T) {
	args := journalctlArgs("orama-node", Options{Since: "-30min", Lines: 5, LinesSet: true})

	if !has(args, "--since=-30min") || !has(args, "-n") {
		t.Errorf("an explicitly requested count was dropped: %v", args)
	}
}

func TestOptions_lineCountByDefault(t *testing.T) {
	args := journalctlArgs("orama-node", Options{Lines: DefaultLines})

	if !has(args, "-n") {
		t.Errorf("no line count and no window: %v — that reads the whole journal", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--since") {
			t.Errorf("a window appeared with no --since: %v", args)
		}
	}
}

// A --since value beginning with '-' is the common one ("-30min"), and it has
// to stay the option's value rather than becoming a flag of its own.
func TestOptions_sinceIsOneArgument(t *testing.T) {
	args := journalctlArgs("orama-node", Options{Since: "-30min"})

	for _, a := range args {
		if a == "-30min" {
			t.Error("--since and its value were passed as two arguments; a value " +
				"starting with '-' would be read as a flag")
		}
	}
}

func TestOptions_followKeepsTheHistory(t *testing.T) {
	args := journalctlArgs("orama-node", Options{Follow: true, Lines: DefaultLines})

	if !has(args, "-f") || !has(args, "-n") {
		t.Errorf("--follow dropped the history: %v — journalctl prints -n lines "+
			"and then follows, so --lines is never silently ignored", args)
	}
}
