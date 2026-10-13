package main

import (
	"encoding/json"
	"strings"
	"testing"

	"orama-demo/host"
)

func visit(t *testing.T, h host.Host, input string) response {
	t.Helper()
	out, err := handle(h, []byte(input))
	if err != nil {
		t.Fatalf("handle(%s): %v", input, err)
	}
	var got response
	if err := json.Unmarshal(out, &got); err != nil || got.Page == "" {
		t.Fatalf("output %s is not a count: %v", out, err)
	}
	return got
}

func TestHandle_everyVisitIsCountedOnce(t *testing.T) {
	h := host.NewFake()

	for want := int64(1); want <= 5; want++ {
		if got := visit(t, h, `{"page":"home"}`); got.Visits != want {
			t.Fatalf("visit %d counted %d", want, got.Visits)
		}
	}
}

func TestHandle_pagesCountSeparatelyAndNoInputIsTheHomePage(t *testing.T) {
	h := host.NewFake()

	visit(t, h, `{"page":"docs"}`)
	visit(t, h, `{"page":"docs"}`)
	got := visit(t, h, ``)

	if got.Page != "home" || got.Visits != 1 {
		t.Errorf("no input counted %+v, want the first visit of home", got)
	}
	if h.Counters["visits:docs"] != 2 {
		t.Errorf("docs counted %d, want 2", h.Counters["visits:docs"])
	}
}

func TestHandle_peekReadsWithoutCounting(t *testing.T) {
	h := host.NewFake()
	visit(t, h, `{"page":"home"}`)

	got := visit(t, h, `{"page":"home","action":"peek"}`)

	if got.Visits != 1 || h.Counters["visits:home"] != 1 {
		t.Errorf("peek answered %d and left the counter at %d, want 1 and 1", got.Visits, h.Counters["visits:home"])
	}
}

func TestHandle_peekOfAPageNoOneVisitedIsZero(t *testing.T) {
	if got := visit(t, host.NewFake(), `{"page":"new","action":"peek"}`); got.Visits != 0 {
		t.Errorf("peek of a new page = %d", got.Visits)
	}
}

func TestHandle_aPageNameThatCouldBecomeAnotherKeyIsRefused(t *testing.T) {
	for _, page := range []string{"Home", "a b", "../x", "a:b", strings.Repeat("a", 33), "-x"} {
		h := host.NewFake()
		body, _ := json.Marshal(map[string]string{"page": page})

		out, err := handle(h, body)

		if err != nil || !strings.Contains(string(out), `"error"`) {
			t.Errorf("page %q: output %s, err %v; want a refusal as an answer", page, out, err)
		}
		if len(h.Counters) != 0 {
			t.Errorf("page %q reached the cache: %v", page, h.Counters)
		}
	}
}

func TestHandle_anUnknownActionIsRefused(t *testing.T) {
	h := host.NewFake()

	out, _ := handle(h, []byte(`{"action":"reset"}`))

	if !strings.Contains(string(out), `"error"`) || len(h.Counters) != 0 {
		t.Errorf("output %s, counters %v", out, h.Counters)
	}
}

func TestHandle_aFailedCacheIsAFailureNotAZeroCount(t *testing.T) {
	h := host.NewFake()
	h.FailCache = true

	if _, err := handle(h, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "cache_incr_by failed") {
		t.Fatalf("err = %v, want the cache failure reported", err)
	}
}

func TestHandle_inputThatIsNotJSONIsAnAnswer(t *testing.T) {
	out, err := handle(host.NewFake(), []byte(`page=home`))

	if err != nil || !strings.Contains(string(out), `"error"`) {
		t.Errorf("output %s, err %v", out, err)
	}
}
