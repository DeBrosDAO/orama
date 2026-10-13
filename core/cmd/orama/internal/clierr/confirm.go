package clierr

import (
	"bufio"
	"io"
	"strings"
)

// Confirm reads one line from in and accepts it when, surrounding space
// trimmed, it is exactly one of accept: a destructive prompt that asks for
// "yes" takes nothing looser, and a [y/N] prompt lists every case it takes.
// Anything else, including end of input, is a declined confirmation and
// returns an Aborted error, so the process exits with CodeAborted rather than
// 0: a script that pipes the wrong answer must not read "nothing happened" as
// success.
func Confirm(in io.Reader, accept ...string) error {
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.TrimSpace(line)
	for _, a := range accept {
		if answer == a {
			return nil
		}
	}
	return Aborted("declined: %q is not a confirmation", answer)
}
