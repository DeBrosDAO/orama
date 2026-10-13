package gotest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// maxLineBytes bounds one line of go test -json output kept for redaction,
// the same bound Parse reads with. A longer line is cut there (and marked)
// rather than failing the redaction.
const maxLineBytes = 8 * 1024 * 1024

// lineBound is maxLineBytes, a variable so tests can shorten it.
var lineBound = maxLineBytes

// readChunk is the read buffer of the redacting stream.
const readChunk = 64 * 1024

// cutMarker ends a line cut at maxLineBytes.
const cutMarker = " …[line cut before redaction]"

// RedactFile rewrites the go test output at path with redact applied: to the
// Output of every test2json event (decoded, so escaped JSON inside it is seen
// as the test printed it) and to every other line whole. It streams line by
// line, cutting a line over maxLineBytes. A missing file is not an error: a
// package that could not start wrote none. On any failure the file is
// replaced by secrets.Withheld: redaction fails closed, never leaving the
// unredacted output behind.
func RedactFile(path string, redact func(string) string) error {
	err := redactStream(path, redact)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if werr := os.WriteFile(path, []byte(secrets.Withheld+"\n"), 0o600); werr != nil {
		return errors.Join(err, fmt.Errorf("failed to withhold %s after its redaction failed: %w", path, werr))
	}
	return fmt.Errorf("%w (its content was withheld)", err)
}

// redactStream writes the redacted lines of path to a temporary file beside
// it and renames that over it.
func redactStream(path string, redact func(string) string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := path + ".redacting"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", tmp, err)
	}
	w := bufio.NewWriter(out)
	rerr := eachLine(bufio.NewReaderSize(in, readChunk), func(line []byte, cut bool) error {
		out := redactLine(line, redact)
		if cut {
			out = append(out, cutMarker...)
		}
		_, err := w.Write(append(out, '\n'))
		return err
	})
	if err := errors.Join(rerr, w.Flush(), out.Close()); err != nil {
		return errors.Join(fmt.Errorf("failed to redact %s: %w", path, err), os.Remove(tmp))
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("failed to replace %s with its redacted copy: %w", path, err)
	}
	return nil
}

// eachLine calls fn with every line of r (without its newline), cutting a
// line over lineBound, discarding the rest of it and saying so (cut).
func eachLine(r *bufio.Reader, fn func(line []byte, cut bool) error) error {
	var line []byte
	cut := false
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read a line: %w", err)
		}
		if !cut {
			line = append(line, chunk...)
			if len(line) > lineBound {
				line, cut = line[:lineBound], true
			}
		}
		if isPrefix {
			continue
		}
		if err := fn(line, cut); err != nil {
			return fmt.Errorf("failed to write a redacted line: %w", err)
		}
		line, cut = line[:0], false
	}
}

// redactLine redacts one line: the Output member of a test2json event, or
// the whole line when it is not one.
func redactLine(line []byte, redact func(string) string) []byte {
	var ev map[string]json.RawMessage
	if json.Unmarshal(line, &ev) != nil {
		return []byte(redact(string(line)))
	}
	if rawOut, ok := ev["Output"]; ok {
		var s string
		if json.Unmarshal(rawOut, &s) != nil {
			return []byte(redact(string(line)))
		}
		enc, err := json.Marshal(redact(s))
		if err != nil {
			return []byte(redact(string(line)))
		}
		ev["Output"] = enc
	}
	re, err := json.Marshal(ev)
	if err != nil {
		return []byte(redact(string(line)))
	}
	// Other members (package, test names) go through redact too, as text.
	return []byte(redact(string(re)))
}
