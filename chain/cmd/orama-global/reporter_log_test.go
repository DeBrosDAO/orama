package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/reporter"
)

func newTestPassLog() (*passLog, *bytes.Buffer) {
	var buf bytes.Buffer
	return newPassLog(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))), &buf
}

func epochErr(epoch uint64, err error) error { return &reporter.EpochError{Epoch: epoch, Err: err} }

func lines(buf *bytes.Buffer, level string) int {
	return strings.Count(buf.String(), "level="+level)
}

// An epoch whose window closed is dropped by the reporter, which is expected
// and not an ERROR; it is logged once for that epoch, however many passes
// report it.
func TestPassLog_aClosedWindowIsWarnedOncePerEpochAndNeverAnError(t *testing.T) {
	p, buf := newTestPassLog()
	closed := func(epoch uint64) error {
		return epochErr(epoch, fmt.Errorf("%w: it was open until epoch %d", reporter.ErrWindowClosed, epoch+2))
	}
	p.pass(closed(3))
	p.pass(closed(3))
	require.Equal(t, 1, lines(buf, "WARN"))
	p.pass(closed(4))
	require.Equal(t, 2, lines(buf, "WARN"), "the next epoch is logged")
	require.Zero(t, lines(buf, "ERROR"))
	require.Contains(t, buf.String(), "epoch 3")
	require.Contains(t, buf.String(), "epoch 4")
}

func TestPassLog_everyExpectedStateIsWarnedOrInformedNotFailed(t *testing.T) {
	for _, state := range []error{reporter.ErrWindowClosed, reporter.ErrEpochSettled, reporter.ErrEpochTooShort, reporter.ErrEpochMissed} {
		p, buf := newTestPassLog()
		p.pass(errors.Join(epochErr(7, state)))
		require.Equal(t, 1, lines(buf, "WARN"), state.Error())
		require.Zero(t, lines(buf, "ERROR"), state.Error())
	}
	p, buf := newTestPassLog()
	p.pass(epochErr(7, fmt.Errorf("%w: 0 of 1 expected votes", reporter.ErrIncompleteArchive)))
	p.pass(epochErr(7, fmt.Errorf("%w: 1 of 3 expected votes", reporter.ErrIncompleteArchive)))
	require.Equal(t, 1, lines(buf, "INFO"), "waiting for the archive is said once while the count changes")
	require.Zero(t, lines(buf, "ERROR")+lines(buf, "WARN"))
}

// A real failure is an ERROR on every pass it happens, and an expected state
// next to it does not hide it.
func TestPassLog_failuresStayErrors(t *testing.T) {
	p, buf := newTestPassLog()
	failure := errors.New("rpc: connection refused")
	for range 3 {
		p.pass(errors.Join(failure, epochErr(3, reporter.ErrWindowClosed)))
	}
	require.Equal(t, 3, lines(buf, "ERROR"))
	require.Equal(t, 1, lines(buf, "WARN"))
	p.pass(epochErr(9, errors.New("submit chunk 1 of 2: broadcast refused")))
	require.Equal(t, 4, lines(buf, "ERROR"))
}

// A state met again after a pass that did not meet it is news again.
func TestPassLog_nilAndRecurrence(t *testing.T) {
	p, buf := newTestPassLog()
	p.pass(nil)
	require.Empty(t, buf.String())
	p.pass(epochErr(3, reporter.ErrWindowClosed))
	p.pass(nil)
	p.pass(epochErr(3, reporter.ErrWindowClosed))
	require.Equal(t, 2, lines(buf, "WARN"))
}
