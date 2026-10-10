package secrets

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// Withheld replaces text whose redaction failed: fail closed, never pass
// unredacted text on.
const Withheld = "[withheld: redaction failed]"

// MaxWriterLine bounds the line a RedactingWriter holds: a longer one is
// redacted and written in pieces of this size. A secret straddling a cut
// can escape the literal match, so the cut is placed at a whitespace when
// one is near.
const MaxWriterLine = 256 << 10

// cutSearch is how far back from MaxWriterLine a cut looks for whitespace.
const cutSearch = 4 << 10

// RedactingWriter redacts what it is given line by line before passing it
// to the underlying writer. Close writes an unterminated last line.
type RedactingWriter struct {
	mu     sync.Mutex
	w      io.Writer
	redact func(string) string
	buf    bytes.Buffer
}

// NewRedactingWriter returns a writer that passes to w what redact returns
// for each line written to it.
func NewRedactingWriter(w io.Writer, redact func(string) string) *RedactingWriter {
	return &RedactingWriter{w: w, redact: redact}
}

// Write buffers p and writes out every complete line, redacted. It reports
// len(p) on success, as io.Writer requires.
func (rw *RedactingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.buf.Write(p)
	for {
		i := bytes.IndexByte(rw.buf.Bytes(), '\n')
		if i < 0 && rw.buf.Len() <= MaxWriterLine {
			return len(p), nil
		}
		n := i + 1
		if i < 0 || n > MaxWriterLine {
			n = cutAt(rw.buf.Bytes()[:MaxWriterLine])
		}
		if err := rw.emit(rw.buf.Next(n)); err != nil {
			return 0, err
		}
	}
}

// Close writes what is left, redacted. It does not close the underlying writer.
func (rw *RedactingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.buf.Len() == 0 {
		return nil
	}
	return rw.emit(rw.buf.Next(rw.buf.Len()))
}

func (rw *RedactingWriter) emit(chunk []byte) error {
	if _, err := io.WriteString(rw.w, rw.redact(string(chunk))); err != nil {
		return fmt.Errorf("failed to write redacted output: %w", err)
	}
	return nil
}

// cutAt is where an over-long line is cut: after the last whitespace in its
// final cutSearch bytes, or at its end.
func cutAt(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-cutSearch; i-- {
		if b[i] == ' ' || b[i] == '\t' {
			return i + 1
		}
	}
	return len(b)
}
