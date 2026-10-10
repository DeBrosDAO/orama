package privhelper

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ToolJournal reads the last lines of a deployment unit's journal as root.
// The orama user is in neither adm nor systemd-journal, so its own
// journalctl sees no unit's messages; the cluster gateway serves
// `orama app logs` and asks the helper. The one form is
// `journal <deployment unit> <lines>`: no flag, no other unit.
const ToolJournal = "journal"

// MaxJournalLines bounds one read. The helper caps what a tool writes back
// well below what 1000 lines could be, so a larger ask would only be cut.
const MaxJournalLines = 1000

func validateJournal(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("journal takes a deployment unit and a line count, got %q", args)
	}
	if !deployUnit.MatchString(args[0]) {
		return fmt.Errorf("journal is only allowed on deployment units, not %q", args[0])
	}
	if _, err := ParseJournalLines(args[1]); err != nil {
		return err
	}
	return nil
}

// ParseJournalLines parses a line count, 1 to MaxJournalLines.
func ParseJournalLines(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxJournalLines {
		return 0, fmt.Errorf("line count %q must be a number from 1 to %d", s, MaxJournalLines)
	}
	return n, nil
}

// TruncatedNotice is what the helper client writes to stderr when the tool
// wrote more than the helper returns. It is out of band: a journal read's
// stdout is only the journal.
const TruncatedNotice = "orama-privhelper: output truncated"

// DeploymentJournal returns the last lines of unit's journal through the
// helper, and whether the helper had to cut the oldest of them to fit.
// ctx bounds the call; cancelling it kills the client.
func DeploymentJournal(ctx context.Context, unit string, lines int) ([]byte, bool, error) {
	cmd := CommandContext(ctx, ToolJournal, unit, strconv.Itoa(lines))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String() + " " + stdout.String())
		return nil, false, fmt.Errorf("read the journal of %s through %s (check %s is running): %w: %s",
			unit, Path, SocketUnitName, err, detail)
	}
	return stdout.Bytes(), strings.Contains(stderr.String(), TruncatedNotice), nil
}
