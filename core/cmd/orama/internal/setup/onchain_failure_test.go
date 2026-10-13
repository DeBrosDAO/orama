package setup

import (
	"errors"
	"strings"
	"testing"
)

// failedEvents are the failed events of the machine at ip whose detail holds text, with their steps.
func failedEvents(h *harness, ip, text string) []Event {
	h.report.mu.Lock()
	defer h.report.mu.Unlock()
	var out []Event
	for _, e := range h.report.events {
		if e.Node == ip && e.State == StateFailed && strings.Contains(e.Detail, text) {
			out = append(out, e)
		}
	}
	return out
}

func TestRun_aFailedHotKeyFundingIsReportedOnceOnItsStepAndTheOnchainStepPointsAtIt(t *testing.T) {
	h := newHarness()
	h.w.fundHotKeyErr = errors.New("insufficient bank balance")

	_, err := run(t, h, h.opts(ip1))

	if err == nil {
		t.Fatal("a failed funding was swallowed")
	}
	got := failedEvents(h, ip1, "insufficient bank balance")
	if len(got) != 1 || got[0].Step != StepHotKey {
		t.Fatalf("the reason is reported %d times, on %+v; want once, on the hot key step", len(got), got)
	}
	if !h.report.has(ip1, StepOnchain, StateFailed) {
		t.Fatal("the on-chain step, which was running, is left unfinished")
	}
	for _, e := range h.report.events {
		if e.Node == ip1 && e.Step == StepOnchain && e.State == StateFailed && !strings.Contains(e.Detail, "hotkey step") {
			t.Errorf("the on-chain failure %q does not say where to read the reason", e.Detail)
		}
	}
}

func TestRun_aFeeBalanceThatCannotBeReadIsReportedOnceToo(t *testing.T) {
	h := newHarness()
	h.w.feeBalanceErr = errors.New("rpc timeout")

	_, err := run(t, h, h.opts(ip1))

	if err == nil || !strings.Contains(err.Error(), "rpc timeout") {
		t.Fatalf("err = %v", err)
	}
	if got := failedEvents(h, ip1, "rpc timeout"); len(got) != 1 || got[0].Step != StepHotKey {
		t.Fatalf("the reason is reported on %+v; want once, on the hot key step", got)
	}
	if !h.report.has(ip1, StepOnchain, StateFailed) {
		t.Error("the on-chain step is left unfinished")
	}
}

func TestRun_aFailureOfAnotherOnchainStepIsStillReportedOnTheOnchainStep(t *testing.T) {
	d := failureDetail(errors.New("register node \"alice\": denied"))
	if !strings.Contains(d, "register node") {
		t.Errorf("detail %q lost the error of a step that reports nothing itself", d)
	}
	wrapped := errors.Join(errors.New("context"), &stepFailure{StepHotKey, errors.New("x")})
	if got := failureDetail(wrapped); !strings.Contains(got, "hotkey step") {
		t.Errorf("detail %q for an error that went through wrapping", got)
	}
}

func TestTextReporter_aHotKeyFailureIsPrintedOnce(t *testing.T) {
	h := newHarness()
	h.w.fundHotKeyErr = errors.New("insufficient bank balance")
	_, _ = run(t, h, h.opts(ip1))
	var out strings.Builder
	text := &TextReporter{Out: &out}
	h.report.mu.Lock()
	for _, e := range h.report.events {
		text.Emit(e)
	}
	h.report.mu.Unlock()

	if n := strings.Count(out.String(), "insufficient bank balance"); n != 1 {
		t.Errorf("the reason is printed %d times:\n%s", n, out.String())
	}
}
