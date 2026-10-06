// Package clierr gives CLI failures an exit code that says what went wrong.
//
// Every failure used to be os.Exit(1) from inside a handler. Three things
// followed from that. A script could not tell "you typed the flag wrong" from
// "the cluster lost quorum", so every failure had to be diagnosed by reading
// the message. Deferred cleanup never ran, which is how a push left staged
// private keys in a temp directory. And nothing was testable: a handler that
// exits cannot be called from a test.
//
// Handlers return an error. Only main decides the process's fate.
package clierr

import (
	"errors"
	"fmt"
	"os"
)

// Exit codes. The set is deliberately small: a code is only worth having when a
// caller would do something different because of it.
const (
	// CodeOK is success.
	CodeOK = 0
	// CodeFailure is the default for an error that names no code. Something
	// went wrong and the message says what.
	CodeFailure = 1
	// CodeUsage means the command line was wrong: a missing flag, a bad value,
	// an unknown subcommand. Retrying unchanged cannot help.
	CodeUsage = 2
	// CodeAuth means the caller is not authenticated or not permitted.
	// `orama auth login` is the fix.
	CodeAuth = 3
	// CodeNotFound means the named thing does not exist.
	CodeNotFound = 4
	// CodeUnavailable means the gateway or a node could not be reached. The
	// request may well succeed later, so a script may retry.
	CodeUnavailable = 5
	// CodeConflict means the cluster refused because doing it would break an
	// invariant — most often quorum. Retrying unchanged will be refused again.
	CodeConflict = 6
	// CodeAborted means the operator declined a confirmation. Nothing happened
	// and nothing is wrong.
	CodeAborted = 7
)

// Error is a failure that carries an exit code.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// CodeOf returns the exit code an error asks for, or CodeFailure.
//
// It looks through wrapping, so a handler may add context with %w without
// losing the classification the layer below made.
func CodeOf(err error) int {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeFailure
}

// withCode builds an *Error from a format string.
func withCode(code int, format string, args ...any) error {
	return &Error{Code: code, Err: fmt.Errorf(format, args...)}
}

// Usage reports a command line that cannot work as written.
func Usage(format string, args ...any) error { return withCode(CodeUsage, format, args...) }

// Auth reports a missing, expired or insufficient credential.
func Auth(format string, args ...any) error { return withCode(CodeAuth, format, args...) }

// NotFound reports that the named thing does not exist.
func NotFound(format string, args ...any) error { return withCode(CodeNotFound, format, args...) }

// Unavailable reports that a gateway or node could not be reached.
func Unavailable(format string, args ...any) error {
	return withCode(CodeUnavailable, format, args...)
}

// Conflict reports a refusal that protects an invariant, such as quorum.
func Conflict(format string, args ...any) error { return withCode(CodeConflict, format, args...) }

// Aborted reports that the operator declined a confirmation.
func Aborted(format string, args ...any) error { return withCode(CodeAborted, format, args...) }

// Failure reports an error with no more specific classification.
func Failure(format string, args ...any) error { return withCode(CodeFailure, format, args...) }

// Wrap attaches a code to an existing error, keeping it unwrappable.
//
// Use this where the underlying error is worth preserving — a caller may still
// want errors.Is against it — and only the classification is being added.
func Wrap(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// RequireRoot returns a usage error when the process is not running as root.
//
// what names the operation, as in "starting the node services". Ten commands
// each wrote this check with their own message and their own os.Exit; the
// message an operator sees for the same mistake should not depend on which
// command they typed.
func RequireRoot(what string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	return Usage("%s must be run as root; re-run with sudo", what)
}
