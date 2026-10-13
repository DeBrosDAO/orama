package monitor

import (
	"errors"
	"strings"
	"testing"
)

func collectSSE(t *testing.T, body string) ([]sseEvent, int) {
	t.Helper()
	var events []sseEvent
	activity := 0
	err := readSSE(strings.NewReader(body), func() { activity++ }, func(ev sseEvent) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("readSSE: %v", err)
	}
	return events, activity
}

func TestReadSSE_snapshotAndErrorEvents(t *testing.T) {
	body := "event: snapshot\ndata: {\"nodes\":[]}\n\n" +
		"event: error\ndata: {\"error\":\"peer 10.0.0.2 timed out\"}\n\n"
	events, _ := collectSSE(t, body)
	if len(events) != 2 {
		t.Fatalf("got %d events: %+v", len(events), events)
	}
	if events[0].Name != eventSnapshot || events[0].Data != `{"nodes":[]}` {
		t.Fatalf("event 0 = %+v", events[0])
	}
	if events[1].Name != eventError || !strings.Contains(events[1].Data, "timed out") {
		t.Fatalf("event 1 = %+v", events[1])
	}
}

func TestReadSSE_multiLineDataIsJoinedWithNewlines(t *testing.T) {
	events, _ := collectSSE(t, "event: snapshot\ndata: line one\ndata:line two\ndata: \n\n")
	if len(events) != 1 || events[0].Data != "line one\nline two\n" {
		t.Fatalf("got %+v", events)
	}
}

func TestReadSSE_keepalivesCountAsActivityButNotEvents(t *testing.T) {
	events, activity := collectSSE(t, ": keepalive\n\n: keepalive\n\n")
	if len(events) != 0 {
		t.Fatalf("keepalives were dispatched as events: %+v", events)
	}
	if activity != 4 {
		t.Fatalf("activity = %d, want every line counted", activity)
	}
}

func TestReadSSE_edgeCases(t *testing.T) {
	cases := map[string]struct {
		body string
		want []sseEvent
	}{
		"no event name is a message": {"data: x\n\n", []sseEvent{{Name: defaultSSEEvent, Data: "x"}}},
		"crlf line endings":          {"event: snapshot\r\ndata: x\r\n\r\n", []sseEvent{{Name: eventSnapshot, Data: "x"}}},
		"event without data":         {"event: snapshot\n\n", nil},
		"cut off at the end":         {"event: snapshot\ndata: x\n", nil},
		"empty body":                 {"", nil},
		"unknown fields ignored":     {"id: 7\nretry: 100\ndata: x\n\n", []sseEvent{{Name: defaultSSEEvent, Data: "x"}}},
		"name resets between events": {"event: error\ndata: a\n\ndata: b\n\n",
			[]sseEvent{{Name: eventError, Data: "a"}, {Name: defaultSSEEvent, Data: "b"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, _ := collectSSE(t, tc.body)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("event %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A snapshot is one data line, and a real cluster's is far past bufio's
// default 64 KiB token.
func TestReadSSE_largeDataLine(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	events, _ := collectSSE(t, "event: snapshot\ndata: "+big+"\n\n")
	if len(events) != 1 || len(events[0].Data) != len(big) {
		t.Fatalf("a 1 MiB data line did not come through whole")
	}
}

func TestReadSSE_handlerErrorStopsTheRead(t *testing.T) {
	stop := errors.New("stop")
	calls := 0
	err := readSSE(strings.NewReader("data: a\n\ndata: b\n\n"), func() {}, func(sseEvent) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want the handler's error after one", err, calls)
	}
}

func TestReadSSE_unterminatedEventIsBounded(t *testing.T) {
	line := "data: " + strings.Repeat("x", 1<<20) + "\n"
	body := strings.Repeat(line, maxSSEEventBytes/(1<<20)+1)
	err := readSSE(strings.NewReader(body), func() {}, func(sseEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("an event past the bound was accepted: %v", err)
	}
}
