// Package printer is how CLI commands write their output.
//
// Output used to go straight to the process's stdout with fmt.Print, with
// emoji and colour written unconditionally. That made three things true at
// once: nothing could be captured in a test, `orama ... | grep` and CI logs got
// escape sequences and emoji that mean nothing there, and only three commands
// could produce machine-readable output because each one had implemented
// --json for itself.
//
// A Printer knows where it is writing and whether that is a terminal, so the
// same command produces a table for a person and JSON for a script.
package printer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/mattn/go-isatty"
)

// Printer writes one command's output.
type Printer struct {
	out io.Writer
	err io.Writer

	// tty is whether out is a terminal a person is reading.
	tty bool
	// json is whether the caller asked for machine-readable output.
	json bool
}

// New returns a Printer writing to out and err.
//
// Whether out is a terminal is decided once, here, rather than at each call:
// the answer cannot change during a command, and asking repeatedly is how
// half a command's output ends up styled and the other half not.
func New(out, errw io.Writer) *Printer {
	return &Printer{out: out, err: errw, tty: isTerminal(out)}
}

// Std returns a Printer writing to the process's stdout and stderr.
func Std() *Printer { return New(os.Stdout, os.Stderr) }

// WithJSON returns a copy that emits JSON instead of tables and status lines.
func (p *Printer) WithJSON(enabled bool) *Printer {
	clone := *p
	clone.json = enabled
	return &clone
}

// JSONMode reports whether the caller asked for machine-readable output.
func (p *Printer) JSONMode() bool { return p.json }

// Out is where a command's own output goes.
func (p *Printer) Out() io.Writer { return p.out }

// Err is where diagnostics go, so they do not land in a piped stdout.
func (p *Printer) Err() io.Writer { return p.err }

// isTerminal reports whether w is a terminal.
//
// Anything that is not an *os.File — a buffer in a test, a pipe wrapper — is
// not a terminal, which is the answer that makes output reproducible.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		// https://no-color.org: any value, including empty-but-set, disables
		// colour. The check is for presence, not for a truthy value.
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// Status symbols. On a terminal these are the emoji people read at a glance;
// anywhere else they are ASCII, because a CI log or a grep does not render
// them and a lone replacement character is worse than a word.
type symbol struct{ pretty, plain string }

var (
	symOK    = symbol{"✅", "OK"}
	symFail  = symbol{"❌", "ERROR"}
	symWarn  = symbol{"⚠️", "WARN"}
	symInfo  = symbol{"ℹ️", "-"}
	symArrow = symbol{"→", "->"}
)

func (p *Printer) render(s symbol) string {
	if p.tty {
		return s.pretty
	}
	return s.plain
}

// Ok reports that something succeeded.
func (p *Printer) Ok(format string, args ...any) {
	p.line(p.out, symOK, format, args...)
}

// Info states a fact the operator wants to see.
func (p *Printer) Info(format string, args ...any) {
	p.line(p.out, symInfo, format, args...)
}

// Warn reports something the operator should notice, on stderr so it does not
// contaminate a piped stdout.
func (p *Printer) Warn(format string, args ...any) {
	p.line(p.err, symWarn, format, args...)
}

// Fail reports a failure on stderr. It does not exit: only main decides that.
func (p *Printer) Fail(format string, args ...any) {
	p.line(p.err, symFail, format, args...)
}

// Arrow renders a transition, as in "devnet → testnet".
func (p *Printer) Arrow() string { return p.render(symArrow) }

func (p *Printer) line(w io.Writer, s symbol, format string, args ...any) {
	if p.json {
		// A status line is for a person. In JSON mode the caller writes one
		// document, and a stray line would make it unparseable.
		return
	}
	fmt.Fprintf(w, "%s %s\n", p.render(s), fmt.Sprintf(format, args...))
}

// Printf writes plain output with no symbol.
func (p *Printer) Printf(format string, args ...any) {
	if p.json {
		return
	}
	fmt.Fprintf(p.out, format, args...)
}

// JSON writes v as an indented JSON document.
func (p *Printer) JSON(v any) error {
	enc := json.NewEncoder(p.out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write JSON output: %w", err)
	}
	return nil
}

// Table writes a header row and its rows, aligned.
//
// In JSON mode it writes the same data as an array of objects keyed by the
// headers, so one call site serves both audiences and they cannot drift.
func (p *Printer) Table(headers []string, rows [][]string) error {
	if p.json {
		return p.JSON(tableToObjects(headers, rows))
	}

	w := tabwriter.NewWriter(p.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	return nil
}

// tableToObjects turns rows into one object per row, keyed by lowercased
// header with spaces as underscores, so `NODE ID` becomes `node_id`.
func tableToObjects(headers []string, rows [][]string) []map[string]string {
	keys := make([]string, len(headers))
	for i, h := range headers {
		keys[i] = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(h)), " ", "_")
	}

	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		obj := make(map[string]string, len(keys))
		for i, key := range keys {
			if i < len(row) {
				obj[key] = row[i]
			}
		}
		out = append(out, obj)
	}
	return out
}
