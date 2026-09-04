package logs

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// DefaultLines is how much history `orama node logs` shows when neither
// --lines nor --since says otherwise.
const DefaultLines = 50

// Options are the ways to choose how much journal to read.
//
// Lines bounds the output to the last N entries. Since bounds it by time and is
// passed to journalctl verbatim ("-30min", "2 hours ago", a timestamp). When
// Since is set, Lines applies only if the caller asked for it — a diagnostic
// that greps for a line that fires once a minute wants a window, not a count.
type Options struct {
	Follow   bool
	Lines    int
	LinesSet bool
	Since    string
}

// Run streams one service's journal.
//
// The unit is resolved rather than passed through, so an alias, a plain unit
// name and a template instance ("orama-namespace-olric@anchat") all work and an
// unknown name is refused before journalctl is asked for a unit that cannot
// exist.
func Run(serviceAlias string, opts Options) error {
	unit, err := utils.ResolveServiceName(serviceAlias)
	if err != nil {
		return err
	}

	if opts.Follow {
		fmt.Fprintf(os.Stderr, "Following logs for %s (press Ctrl+C to stop)...\n\n", unit)
	}

	cmd := exec.Command("journalctl", journalctlArgs(unit, opts)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		// A journalctl killed by a signal is never a failure worth reporting:
		// Ctrl+C is how a follow ends, and `orama node logs node | head` sends
		// SIGPIPE to a read the caller deliberately cut short.
		if isInterrupted(cmd) {
			return nil
		}
		return fmt.Errorf("reading the journal for %s failed: %w", unit, err)
	}
	return nil
}

// isInterrupted reports whether the process was ended by a signal rather than
// by exiting on its own.
func isInterrupted(cmd *exec.Cmd) bool {
	state := cmd.ProcessState
	return state != nil && !state.Exited()
}

// journalctlArgs is the argument list a read of one unit's journal needs.
//
// --since and -n together silently truncate the window to the last N entries
// inside it, so a window replaces the count unless the caller asked for both.
func journalctlArgs(unit string, opts Options) []string {
	args := []string{"-u", unit, "--no-pager"}
	if opts.Since != "" {
		// One argv element, so a value beginning with '-' — "-30min" is the
		// common one — is the option's value and never a flag of its own.
		args = append(args, "--since="+opts.Since)
	}
	if opts.Since == "" || opts.LinesSet {
		args = append(args, "-n", strconv.Itoa(opts.Lines))
	}
	if opts.Follow {
		args = append(args, "-f")
	}
	return args
}
