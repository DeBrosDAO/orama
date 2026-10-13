package gateway

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestPhases_eachStepIsTimedFromTheOneBefore(t *testing.T) {
	start := time.Date(2026, 10, 4, 15, 58, 36, 0, time.UTC)
	p := &requestPhases{start: start, marks: []phaseMark{
		{name: "routing", at: start.Add(2 * time.Millisecond)},
		{name: "auth", at: start.Add(3200 * time.Millisecond)},
		{name: "upstream", at: start.Add(3250 * time.Millisecond)},
	}}
	got := map[string]int64{}
	for _, f := range p.fields(start.Add(3260 * time.Millisecond)) {
		got[f.Key] = f.Integer
	}
	want := map[string]int64{"routing_ms": 2, "auth_ms": 3198, "upstream_ms": 50, "rest_ms": 10}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

func TestRequestPhases_noMarksIsAllRest(t *testing.T) {
	start := time.Now()
	fields := (&requestPhases{start: start}).fields(start.Add(time.Second))
	if len(fields) != 1 || fields[0].Key != "rest_ms" || fields[0].Integer != 1000 {
		t.Fatalf("fields %v, want only rest_ms=1000", fields)
	}
}

func TestMarkPhase_aRequestWithoutPhasesIsLeftAlone(t *testing.T) {
	markPhase(httptest.NewRequest("GET", "/", nil), "auth") // must not panic

	r, p := withRequestPhases(httptest.NewRequest("GET", "/", nil), time.Now())
	markPhase(r, "auth")
	if len(p.marks) != 1 || p.marks[0].name != "auth" {
		t.Fatalf("marks %v, want one auth mark", p.marks)
	}
}
