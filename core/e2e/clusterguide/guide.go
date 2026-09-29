// Package clusterguide executes docs/RUN_YOUR_OWN_CLUSTER.md against real
// machines. It parses the guide's command blocks, matches them one by one
// against the steps in plan.go, substitutes the guide's example values with the
// fixture's, and runs them. A guide that gains, loses, reorders or renames a
// command, or drops a flag a step needs, fails the run before anything is
// executed; a step the guide does not have fails it too.
//
// The parser and runner are plain Go with a fake-able executor and are
// unit-tested in every `go test ./...`. The run against machines is
// cluster_e2e_test.go, behind the e2e_cluster build tag (make e2e-cluster).
package clusterguide

import (
	"fmt"
	"strings"
)

// Command is one command line of the guide.
type Command struct {
	// Section is the guide's "## " heading the command sits under.
	Section string
	// Line is the command as written, continuations joined.
	Line string
	Argv []string
}

// knownPrograms are the programs the guide may run. A command outside this
// list is an error, so a new kind of step has to be taught to the harness.
var knownPrograms = map[string]bool{"orama": true, "rw": true}

// ParseGuide returns every command in the guide's shell blocks, in order.
func ParseGuide(markdown string) ([]Command, error) {
	var (
		cmds    []Command
		section string
		inShell bool
		inOther bool
		pending string
	)
	for n, raw := range strings.Split(markdown, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "```"):
			if pending != "" {
				return nil, fmt.Errorf("line %d: a command continues past the end of its block: %q", n+1, pending)
			}
			if inShell || inOther {
				inShell, inOther = false, false
				continue
			}
			lang := strings.TrimPrefix(line, "```")
			inShell = lang == "bash" || lang == "sh"
			inOther = !inShell
		case inShell:
			cmd, rest, err := shellLine(line, pending, section)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			pending = rest
			if cmd != nil {
				cmds = append(cmds, *cmd)
			}
		case !inOther && strings.HasPrefix(line, "## "):
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
		}
	}
	if pending != "" {
		return nil, fmt.Errorf("the guide ends inside a command: %q", pending)
	}
	return cmds, nil
}

// shellLine folds one line of a shell block into the command being built.
// It returns a finished command, or the text still waiting for its
// continuation.
func shellLine(line, pending, section string) (*Command, string, error) {
	if pending == "" && (line == "" || strings.HasPrefix(line, "#")) {
		return nil, "", nil
	}
	if strings.HasSuffix(line, `\`) {
		return nil, strings.TrimSpace(pending + " " + strings.TrimSuffix(line, `\`)), nil
	}
	full := strings.TrimSpace(pending + " " + line)
	argv, err := splitArgs(full)
	if err != nil {
		return nil, "", fmt.Errorf("%w in %q", err, full)
	}
	if !knownPrograms[argv[0]] {
		return nil, "", fmt.Errorf("the guide runs %q, which the harness does not know how to run: teach it in plan.go", argv[0])
	}
	return &Command{Section: section, Line: full, Argv: argv}, "", nil
}

// splitArgs splits a command line on spaces, honouring single and double
// quotes, and drops a trailing "# comment". It is not a shell: no expansion,
// no operators.
func splitArgs(s string) ([]string, error) {
	var (
		args  []string
		cur   strings.Builder
		quote rune
		have  bool
	)
	prevSpace := true
	for _, r := range s {
		startsComment := r == '#' && quote == 0 && prevSpace
		prevSpace = r == ' ' || r == '\t'
		if startsComment {
			break
		}
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, have = r, true
		case r == ' ' || r == '\t':
			if have || cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
				have = false
			}
		case r == '|' || r == ';' || r == '&' || r == '$' || r == '`':
			return nil, fmt.Errorf("shell syntax %q is not supported by the harness", string(r))
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if have || cur.Len() > 0 {
		args = append(args, cur.String())
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return args, nil
}

// Flags returns the flag names in argv (without values or a "=value"), in the
// order given.
func Flags(argv []string) []string {
	var out []string
	for _, a := range argv {
		if strings.HasPrefix(a, "--") {
			name, _, _ := strings.Cut(a, "=")
			out = append(out, name)
		}
	}
	return out
}
