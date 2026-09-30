package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testFetcher(srv *httptest.Server) HTTPFetcher {
	return HTTPFetcher{
		Client:  srv.Client(),
		BaseURL: func(string) string { return srv.URL },
		Sign: func(r *http.Request, audience string) error {
			r.Header.Set("X-Test-Signed", "yes")
			r.Header.Set("X-Test-Audience", audience)
			return nil
		},
	}
}

func TestHTTPFetcherFetch_signedRequestAndReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != InternalReportPath || r.Header.Get("X-Test-Signed") != "yes" || r.Header.Get("X-Test-Audience") != "p" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set(ReportAgeHeader, "2500")
		// A peer whose clock runs 30s ahead.
		w.Header().Set(ClockHeader, strconv.FormatInt(time.Now().Add(30*time.Second).UnixMilli(), 10))
		w.Write([]byte(`{"hostname":"n2","version":"0.200.0"}`))
	}))
	defer srv.Close()
	pr, err := testFetcher(srv).Fetch(context.Background(), Peer{ID: "p", WGIP: "10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Report.Hostname != "n2" || pr.Report.Version != "0.200.0" || pr.Age != 2500*time.Millisecond {
		t.Fatalf("report = %+v age %v", pr.Report, pr.Age)
	}
	if off := pr.ClockOffset; !pr.ClockMeasured || off < 29*time.Second || off > 31*time.Second {
		t.Fatalf("clock offset = %v measured=%v, want about 30s", off, pr.ClockMeasured)
	}
}

func TestHTTPFetcherFetch_errors(t *testing.T) {
	cases := map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"non-200": {func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no report yet", 503) }, "HTTP 503: no report yet"},
		"refused": {func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "not found", 404) }, "cluster secret"},
		"no age":  {func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{}`)) }, ReportAgeHeader},
		"huge age": {func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(ReportAgeHeader, "9223372036854775807")
			w.Write([]byte(`{}`))
		}, ReportAgeHeader},
		"bad json": {func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{")) }, "parse telemetry"},
		"too big": {func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"hostname":"` + strings.Repeat("x", maxReportBytes) + `"}`))
		}, "over"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			_, err := testFetcher(srv).Fetch(context.Background(), Peer{ID: "p", WGIP: "10.0.0.2"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestHTTPFetcherFetch_oldReleaseIsWithoutTelemetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing credential", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := testFetcher(srv).Fetch(context.Background(), Peer{ID: "p", WGIP: "10.0.0.2"})
	if !errors.Is(err, ErrPeerWithoutTelemetry) {
		t.Fatalf("err = %v, want ErrPeerWithoutTelemetry", err)
	}
}

// A signed request goes only to a mesh address: a registry row pointing at a
// public or empty address must not send one off the overlay.
func TestHTTPFetcherFetch_refusesAddressOffTheMesh(t *testing.T) {
	for _, ip := range []string{"", "37.59.116.212", "10.0.1.5", "not-an-ip"} {
		_, err := HTTPFetcher{}.Fetch(context.Background(), Peer{ID: "p", WGIP: ip})
		if err == nil || !strings.Contains(err.Error(), "no WireGuard address") {
			t.Errorf("WGIP %q: err = %v", ip, err)
		}
	}
}

func TestHTTPFetcherFetch_signingFailureStopsRequest(t *testing.T) {
	f := HTTPFetcher{
		Client:  http.DefaultClient,
		BaseURL: func(string) string { return "http://127.0.0.1:1" },
		Sign:    func(*http.Request, string) error { return errors.New("no cluster secret") },
	}
	if _, err := f.Fetch(context.Background(), Peer{WGIP: "10.0.0.2"}); err == nil || !strings.Contains(err.Error(), "no cluster secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine([]byte("one\ntwo")); got != "one" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine(nil); got != "" {
		t.Errorf("firstLine(nil) = %q", got)
	}
}

// A peer on the release before the clock header answers without it: its
// report still counts, and its clock is simply not measured.
func TestHTTPFetcherFetch_noClockHeaderKeepsReportUnmeasured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(ReportAgeHeader, "10")
		w.Write([]byte(`{"hostname":"n3"}`))
	}))
	defer srv.Close()
	pr, err := testFetcher(srv).Fetch(context.Background(), Peer{ID: "p", WGIP: "10.0.0.3"})
	if err != nil || pr.Report == nil || pr.Report.Hostname != "n3" {
		t.Fatalf("pr=%+v err=%v, want the report kept", pr, err)
	}
	if pr.ClockMeasured {
		t.Fatal("a missing clock header read as a measured clock")
	}
}

func TestClockOffset_hugeValueStaysFinite(t *testing.T) {
	now := time.Now()
	off, ok := clockOffset("9223372036854775807", now, now)
	if !ok || off <= 0 {
		t.Fatalf("offset = %v ok=%v, want a large positive offset (the skew alert reports it)", off, ok)
	}
	if _, ok := clockOffset("not-a-number", now, now); ok {
		t.Fatal("garbage parsed as a clock")
	}
}
