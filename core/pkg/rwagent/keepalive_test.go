package rwagent

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// A rolling upgrade fetches every SSH key up front and then spends
// twenty-five minutes or more in SSH sessions the agent never sees. Its
// auto-lock is a sliding thirty-minute window that only its own traffic resets,
// so it locked partway through and the next thing that needed it — the health
// gate between two nodes — stopped and waited for an unlock prompt.

// countingAgent answers /v1/status and counts the calls.
func countingAgent(t *testing.T) (*Client, func() int) {
	t.Helper()

	var mu sync.Mutex
	calls := 0

	client := agentStub(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"data":{"version":"1","locked":false,"uptime":1,"pid":1,"connectedApps":1,"pendingUnlocks":0}}`))
	})

	return client, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

func TestKeepUnlockedTouchesTheAgent(t *testing.T) {
	client, calls := countingAgent(t)

	stop := client.KeepUnlocked(10 * time.Millisecond)
	defer stop()

	deadline := time.Now().Add(2 * time.Second)
	for calls() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if got := calls(); got < 3 {
		t.Errorf("the agent was touched %d times in 2s at a 10ms interval — the window is not being held open", got)
	}
}

// The wallet must not stay awake once the work that needed it is done.
func TestKeepUnlockedStopsWhenTold(t *testing.T) {
	client, calls := countingAgent(t)

	stop := client.KeepUnlocked(10 * time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	stop()

	atStop := calls()
	time.Sleep(120 * time.Millisecond)

	if after := calls(); after != atStop {
		t.Errorf("the agent was touched %d more times after stop — the wallet is being held open past the operation", after-atStop)
	}
}

func TestKeepUnlockedStopIsIdempotent(t *testing.T) {
	client, _ := countingAgent(t)

	stop := client.KeepUnlocked(10 * time.Millisecond)
	stop()
	// A cleanup function can be called twice — a defer plus an explicit call on
	// an error path — and must not block or panic.
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the second stop blocked")
	}
}

// Pinging a socket that is not there every five minutes tells nobody anything.
// Whatever needs the agent next reports it properly.
func TestKeepUnlockedGivesUpOnAStoppedAgent(t *testing.T) {
	client := New(missingSocket(t))

	stop := client.KeepUnlocked(10 * time.Millisecond)
	defer stop()

	// It returns on its own; stop must still return promptly rather than
	// waiting on a goroutine that has already finished.
	time.Sleep(80 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop blocked after the keepalive gave up")
	}
}

func TestKeepUnlockedDefaultsItsInterval(t *testing.T) {
	client, calls := countingAgent(t)

	// A zero or negative interval would make a ticker panic; it falls back.
	stop := client.KeepUnlocked(0)
	defer stop()

	time.Sleep(50 * time.Millisecond)
	if calls() != 0 {
		t.Errorf("a defaulted interval is %s, so nothing should have been sent yet", DefaultKeepaliveInterval)
	}
	if DefaultKeepaliveInterval >= 30*time.Minute {
		t.Errorf("DefaultKeepaliveInterval is %s, which does not fit inside the agent's 30-minute window",
			DefaultKeepaliveInterval)
	}
}
