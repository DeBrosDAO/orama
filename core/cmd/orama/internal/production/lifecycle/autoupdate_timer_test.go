package lifecycle

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

const (
	stopTimer    = "stop orama-autoupdate.timer"
	startTimer   = "start orama-autoupdate.timer"
	stopAgent    = "stop orama-autoupdate.service"
	restartEvent = "restart services"
	servingEvent = "node serving"
)

// timerRun is a restart with a fake systemd: the order things happened in.
type timerRun struct {
	events                 []string
	timerActive            bool
	stopErr, startErr      error
	stopRunErr             error
	restartErr, servingErr error
	// cancelInRestart and cancelInServing cancel the run's context while that step runs, as an
	// interrupt, a terminate or a hangup does.
	cancelInRestart, cancelInServing bool
	// probeErr is what the node answers when the interrupted restart looks at it once.
	probeErr error
	// out is what the run printed.
	out string
}

func (r *timerRun) run(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	units := timerUnits{
		active: func(unit string) bool {
			if unit != systemd.AutoUpdateTimerName {
				t.Errorf("asked whether %s is active", unit)
			}
			return r.timerActive
		},
		stop: func(unit string) error {
			r.events = append(r.events, "stop "+unit)
			return r.stopErr
		},
		start: func(unit string) error {
			r.events = append(r.events, "start "+unit)
			return r.startErr
		},
		stopRun: func(unit string) error {
			r.events = append(r.events, "stop "+unit)
			return r.stopRunErr
		},
	}
	restart := func() error {
		r.events = append(r.events, restartEvent)
		if r.cancelInRestart {
			cancel()
		}
		return r.restartErr
	}
	probes := 0
	serving := func(ctx context.Context) error {
		r.events = append(r.events, servingEvent)
		if r.cancelInServing && probes == 0 {
			cancel()
		}
		if ctx.Err() != nil {
			return errors.New("cancelled while waiting for the node")
		}
		probes++
		if r.cancelInRestart || r.cancelInServing {
			return r.probeErr
		}
		return r.servingErr
	}
	var err error
	r.out = captureStdout(t, func() { err = restartWithAutoUpdatePaused(ctx, units, restart, serving) })
	return err
}

// captureStdout returns what fn printed to standard output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wr
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(rd)
		done <- string(b)
	}()
	defer func() { os.Stdout = old }()
	fn()
	os.Stdout = old
	wr.Close()
	return <-done
}

func wantEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestRestartWithAutoUpdatePaused_stopsTheTimerAndTheAgentBeforeTheServicesAndRestoresTheTimerAfterTheNodeServes(t *testing.T) {
	r := &timerRun{timerActive: true}
	if err := r.run(t); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, r.events, stopTimer, stopAgent, restartEvent, servingEvent, startTimer)
}

func TestRestartWithAutoUpdatePaused_aTimerThatWasOffStaysOffButAnAgentRunIsStopped(t *testing.T) {
	r := &timerRun{timerActive: false}
	if err := r.run(t); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, r.events, stopAgent, restartEvent)
}

func TestRestartWithAutoUpdatePaused_aFailedRestartLeavesTheTimerStoppedAndSaysHowToStartIt(t *testing.T) {
	boom := errors.New("port 53 is in use")
	r := &timerRun{timerActive: true, restartErr: boom}
	err := r.run(t)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the restart's", err)
	}
	wantEvents(t, r.events, stopTimer, stopAgent, restartEvent)
	for _, want := range []string{"orama-autoupdate.timer stays stopped", "systemctl start orama-autoupdate.timer", "not serving"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
}

func TestRestartWithAutoUpdatePaused_aNodeThatDoesNotServeLeavesTheTimerStoppedAndSaysHowToStartIt(t *testing.T) {
	notServing := errors.New("rqlite has no leader")
	r := &timerRun{timerActive: true, servingErr: notServing}
	err := r.run(t)
	if !errors.Is(err, notServing) {
		t.Errorf("err = %v, want the node's", err)
	}
	wantEvents(t, r.events, stopTimer, stopAgent, restartEvent, servingEvent)
	if !strings.Contains(r.out, "systemctl start orama-autoupdate.timer") {
		t.Errorf("output does not say how to start the timer:\n%s", r.out)
	}
}

func TestRestartWithAutoUpdatePaused_aTimerThatCannotBeStoppedStopsTheRestart(t *testing.T) {
	r := &timerRun{timerActive: true, stopErr: errors.New("access denied")}
	err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "orama-autoupdate.timer") {
		t.Errorf("err = %v", err)
	}
	wantEvents(t, r.events, stopTimer)
}

func TestRestartWithAutoUpdatePaused_anAgentRunThatCannotBeStoppedStopsTheRestartAndRestoresTheTimer(t *testing.T) {
	r := &timerRun{timerActive: true, stopRunErr: errors.New("access denied")}
	err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "orama-autoupdate.service") {
		t.Errorf("err = %v", err)
	}
	wantEvents(t, r.events, stopTimer, stopAgent, startTimer)
}

func TestRestartWithAutoUpdatePaused_aTimerThatCannotBeStartedIsReportedWithTheCommand(t *testing.T) {
	r := &timerRun{timerActive: true, startErr: errors.New("unit masked")}
	err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "systemctl start orama-autoupdate.timer") {
		t.Errorf("err = %v, want the command that starts it", err)
	}
}

func TestRestartWithAutoUpdatePaused_anInterruptDuringTheRestartFinishesItAndRestoresTheTimerWhenTheNodeServes(t *testing.T) {
	r := &timerRun{timerActive: true, cancelInRestart: true}
	err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("err = %v, want it to say the run was interrupted", err)
	}
	wantEvents(t, r.events, stopTimer, stopAgent, restartEvent, servingEvent, servingEvent, startTimer)
	if !strings.Contains(r.out, "finishing the restart") {
		t.Errorf("the operator is not told the restart is being finished:\n%s", r.out)
	}
}

func TestRestartWithAutoUpdatePaused_anInterruptDuringTheRestartLeavesTheTimerStoppedWhenTheNodeDoesNotServe(t *testing.T) {
	r := &timerRun{timerActive: true, cancelInRestart: true, probeErr: errors.New("gateway not answering")}
	err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "gateway not answering") || !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("err = %v", err)
	}
	if slices.Contains(r.events, startTimer) {
		t.Errorf("events = %v: the timer was started for a node that does not serve", r.events)
	}
	if !strings.Contains(r.out, "systemctl start orama-autoupdate.timer") {
		t.Errorf("output does not say how to start the timer:\n%s", r.out)
	}
}

func TestRestartWithAutoUpdatePaused_anInterruptWhileWaitingForTheNodeLooksOnceAndRestoresTheTimerIfItServes(t *testing.T) {
	r := &timerRun{timerActive: true, cancelInServing: true}
	err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("err = %v", err)
	}
	wantEvents(t, r.events, stopTimer, stopAgent, restartEvent, servingEvent, servingEvent, startTimer)
}

func TestRestartWithAutoUpdatePaused_anInterruptAfterAFailedRestartReportsTheFailureAndKeepsTheTimerStopped(t *testing.T) {
	boom := errors.New("port 53 is in use")
	r := &timerRun{timerActive: true, cancelInRestart: true, restartErr: boom}
	err := r.run(t)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the restart's", err)
	}
	wantEvents(t, r.events, stopTimer, stopAgent, restartEvent)
}

func TestRestartWithAutoUpdatePaused_anInterruptWithTheTimerOffStillFinishesAndReportsTheNode(t *testing.T) {
	r := &timerRun{timerActive: false, cancelInRestart: true}
	err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("err = %v", err)
	}
	wantEvents(t, r.events, stopAgent, restartEvent, servingEvent)
}
