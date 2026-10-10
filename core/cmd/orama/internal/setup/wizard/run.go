package wizard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

// Start runs the wizard on the terminal and returns how it ended. preset holds
// the flags the person already gave; the questions start from them.
//
// While the run is going, what the legacy code prints with fmt (host-key
// scans, the archive upload) would be drawn over the screen. Standard output and
// error are therefore replaced by a pipe for the length of the program, and each
// line that comes through is shown in the run's output pane.
func Start(ctx context.Context, svc Services, preset setup.Options) (Outcome, error) {
	m := New(ctx, svc, preset)
	realOut := os.Stdout
	restore, err := captureOutput(m)
	if err != nil {
		return Outcome{}, err
	}
	defer restore()
	prog := tea.NewProgram(m, tea.WithOutput(realOut), tea.WithInput(os.Stdin))
	// The program owns the terminal's input. Code that reads os.Stdin while it runs
	// (the ssh sessions of the reused node-setup code inherit it) must not compete
	// for the keys, or hand them to a remote command.
	restoreIn, err := detachStdin()
	if err != nil {
		return Outcome{}, err
	}
	defer restoreIn()
	_, err = prog.Run()
	// Whatever ended the program, a run in flight is cancelled and awaited, so it
	// closes its connections and removes its temporary files before the process exits.
	m.finish()
	if err != nil {
		return Outcome{}, err
	}
	return m.Outcome(), nil
}

// detachStdin points os.Stdin at /dev/null until the returned func is called.
func detachStdin() (restore func(), err error) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	old := os.Stdin
	os.Stdin = null
	return func() {
		os.Stdin = old
		_ = null.Close()
	}, nil
}

// finish waits for a run the person interrupted to end, so that it closes its
// connections and removes its temporary files before the process exits.
func (m *Model) finish() {
	if !m.running {
		return
	}
	m.cancel()
	for msg := range m.feed {
		if d, ok := msg.(doneMsg); ok {
			m.result, m.runErr, m.running = d.res, d.err, false
			m.outcome.Result = d.res
			return
		}
	}
}

// captureOutput points os.Stdout and os.Stderr at a pipe and feeds each line to
// the model. The returned func puts them back.
func captureOutput(m *Model) (restore func(), err error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	done := make(chan struct{})
	go func() {
		defer close(done)
		pump(r, m.Feed)
	}()
	return func() {
		os.Stdout, os.Stderr = oldOut, oldErr
		_ = w.Close()
		select {
		case <-done:
		case <-time.After(drainBudget):
			// A child that outlived the run still holds the write end; closing the read
			// end below ends the pump.
		}
		_ = r.Close()
	}, nil
}

// pump calls feed with each line read from r until it ends.
func pump(r io.Reader, feed func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, pumpInitialBuffer), pumpMaxLine)
	for sc.Scan() {
		feed(sc.Text())
	}
	// A line longer than the limit ends the scan; what follows is still read and
	// dropped, or the command writing to the pipe would block on it for good.
	_, _ = io.Copy(io.Discard, r)
}

const (
	pumpInitialBuffer = 64 << 10
	pumpMaxLine       = 1 << 20
	// drainBudget is how long restoring the streams waits for what is still in the pipe.
	drainBudget = 2 * time.Second
)
