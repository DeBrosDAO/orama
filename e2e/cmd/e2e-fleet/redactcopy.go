package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// copyRedacted writes src to dst (0600) through redact, line by line. A
// missing src is nothing to copy. On failure dst holds secrets.Withheld,
// never a partial unredacted copy.
func copyRedacted(src, dst string, redact func(string) string) error {
	in, err := os.Open(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", dst, err)
	}
	w := secrets.NewRedactingWriter(out, redact)
	_, cerr := io.Copy(w, in)
	if err := errors.Join(cerr, w.Close(), out.Close()); err != nil {
		werr := os.WriteFile(dst, []byte(secrets.Withheld+"\n"), 0o600)
		return errors.Join(fmt.Errorf("failed to copy %s redacted to %s: %w", src, dst, err), werr)
	}
	return nil
}
