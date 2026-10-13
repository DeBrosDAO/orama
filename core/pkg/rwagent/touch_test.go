package rwagent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTouch_UnlockedResetsTheWindow(t *testing.T) {
	client := agentStub(t, rawJSON(200, `{"ok":true,"data":{"locked":false,"autoLockInSeconds":1800,"pendingApprovals":1}}`))

	resp, err := client.Touch(context.Background())
	if err != nil {
		t.Fatalf("touch: %v", err)
	}
	if resp.Locked || resp.AutoLockInSeconds == nil || *resp.AutoLockInSeconds != 1800 {
		t.Errorf("touch = %+v, want unlocked with 1800s left", resp)
	}
	if resp.PendingApprovals != 1 {
		t.Errorf("PendingApprovals = %d, want 1", resp.PendingApprovals)
	}
}

func TestTouch_LockedStaysLocked(t *testing.T) {
	client := agentStub(t, rawJSON(200, `{"ok":true,"data":{"locked":true,"autoLockInSeconds":null,"pendingApprovals":0}}`))

	resp, err := client.Touch(context.Background())
	if err != nil {
		t.Fatalf("touch: %v", err)
	}
	if !resp.Locked || resp.AutoLockInSeconds != nil {
		t.Errorf("touch = %+v, want locked with no deadline", resp)
	}
}

func TestTouch_NotApproved(t *testing.T) {
	client := agentStub(t, rawJSON(403, `{"ok":false,"error":"orama is not an approved RootWallet app","code":"NOT_APPROVED"}`))

	_, err := client.Touch(context.Background())
	if !IsNotApproved(err) {
		t.Fatalf("err = %v, want NOT_APPROVED", err)
	}
	if !strings.Contains(err.Error(), "approve this application") {
		t.Errorf("the error should say how to fix it: %v", err)
	}
}

// An agent that predates the route answers it like any unknown path.
func TestTouch_OldAgentHasNoRoute(t *testing.T) {
	client := agentStub(t, rawJSON(404, `{"ok":false,"error":"unknown route","code":"NOT_FOUND"}`))

	_, err := client.Touch(context.Background())
	if !errors.Is(err, ErrTouchUnsupported) {
		t.Fatalf("err = %v, want ErrTouchUnsupported", err)
	}
}

func TestTouch_OldAgentWithPlainTextNotFound(t *testing.T) {
	client := agentStub(t, rawJSON(404, "404 page not found\n"))

	_, err := client.Touch(context.Background())
	if !errors.Is(err, ErrTouchUnsupported) {
		t.Fatalf("err = %v, want ErrTouchUnsupported", err)
	}
}

func TestTouch_PlainTextForbiddenIsNotApproved(t *testing.T) {
	client := agentStub(t, rawJSON(403, "forbidden\n"))

	_, err := client.Touch(context.Background())
	if !IsNotApproved(err) {
		t.Fatalf("err = %v, want a not-approved error", err)
	}
}

func TestStatus_CarriesPendingApprovalsApartFromUnlocks(t *testing.T) {
	client := agentStub(t, rawJSON(200, `{"ok":true,"data":{
		"version":"1.2.3","locked":true,"uptime":42,"pid":9,
		"connectedApps":1,"pendingUnlocks":2,"pendingApprovals":1}}`))

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.PendingUnlocks != 2 || status.PendingApprovals != 1 {
		t.Errorf("status = %+v, want 2 pending unlocks and 1 pending approval", status)
	}
}

// keepaliveWarnings runs KeepUnlocked against an agent answering with body,
// waits for it to give up, and returns what it wrote to its warning sink.
func keepaliveWarnings(t *testing.T, status int, body string) string {
	t.Helper()
	client := agentStub(t, rawJSON(status, body))
	var warned bytes.Buffer
	client.warn = &warned

	stop := client.KeepUnlocked(5 * time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	stop()
	return warned.String()
}

// Degrading silently would leave someone wondering why a rollout stopped at an
// unlock prompt. It says so, once, and stops touching an agent that can't help.
func TestKeepUnlocked_WarnsOnceWhenTheAgentIsTooOld(t *testing.T) {
	got := keepaliveWarnings(t, 404, `{"ok":false,"error":"unknown route","code":"NOT_FOUND"}`)
	if strings.Count(got, "warning:") != 1 {
		t.Fatalf("want exactly one warning, got %q", got)
	}
	if !strings.Contains(got, "Update RootWallet") {
		t.Errorf("the warning should say what to do: %q", got)
	}
}

func TestKeepUnlocked_WarnsOnceWhenNotApproved(t *testing.T) {
	got := keepaliveWarnings(t, 403, `{"ok":false,"error":"orama is not an approved RootWallet app","code":"NOT_APPROVED"}`)
	if strings.Count(got, "warning:") != 1 {
		t.Fatalf("want exactly one warning, got %q", got)
	}
	if !strings.Contains(got, "approve this application") {
		t.Errorf("the warning should name the missing approval: %q", got)
	}
}

func TestKeepUnlocked_StaysQuietWhileItWorks(t *testing.T) {
	got := keepaliveWarnings(t, 200, `{"ok":true,"data":{"locked":false,"autoLockInSeconds":1800,"pendingApprovals":0}}`)
	if got != "" {
		t.Errorf("a working keepalive should print nothing, got %q", got)
	}
}
