package namespace

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A restarted gateway is active in systemd long before it binds; the wait must
// ride out the refused connections and return once /v1/health answers.
func TestAwaitGatewayServing_waitsForTheListener(t *testing.T) {
	orig := overlayIP
	overlayIP = func() (string, error) { return "127.0.0.1", nil }
	t.Cleanup(func() { overlayIP = orig })

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() // nothing listens yet, as in the seconds after a gateway restart

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"status":"ok"}`)
	})}
	t.Cleanup(func() { srv.Close() })
	bound := make(chan struct{})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			close(bound)
			return
		}
		close(bound)
		srv.Serve(l2)
	}()

	if err := awaitGatewayServing(t.Context(), "tenant", port, 10*time.Second); err != nil {
		t.Fatalf("gateway that binds after 1.5s was not awaited: %v", err)
	}
	<-bound
}

func TestAwaitGatewayServing_neverBinds(t *testing.T) {
	orig := overlayIP
	overlayIP = func() (string, error) { return "127.0.0.1", nil }
	t.Cleanup(func() { overlayIP = orig })

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	err = awaitGatewayServing(t.Context(), "tenant", port, 2*time.Second)
	if err == nil {
		t.Fatal("a gateway that never binds reported as serving")
	}
	if !strings.Contains(err.Error(), "tenant gateway") {
		t.Errorf("error does not name the gateway: %v", err)
	}
}

func TestAwaitGatewayServing_unhealthyIsNotServing(t *testing.T) {
	orig := overlayIP
	overlayIP = func() (string, error) { return "127.0.0.1", nil }
	t.Cleanup(func() { overlayIP = orig })

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	if err := awaitGatewayServing(t.Context(), "tenant", l.Addr().(*net.TCPAddr).Port, 2*time.Second); err == nil {
		t.Fatal("a gateway answering 503 reported as serving")
	}
}

func serveHealth(t *testing.T, code int, body string) int {
	t.Helper()
	orig := overlayIP
	overlayIP = func() (string, error) { return "127.0.0.1", nil }
	t.Cleanup(func() { overlayIP = orig })
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// A degraded gateway answers /v1/health with 503 but is up and serving; failing
// the restart over it was the bug.
func TestAwaitGatewayServing_degradedIsServing(t *testing.T) {
	for _, status := range []string{"degraded", "unhealthy"} {
		port := serveHealth(t, http.StatusServiceUnavailable, `{"status":"`+status+`"}`)
		if err := awaitGatewayServing(t.Context(), "tenant", port, 2*time.Second); err != nil {
			t.Errorf("%s gateway not treated as serving: %v", status, err)
		}
	}
}

func TestAwaitGatewayServing_startingAndBlockedAreNotServing(t *testing.T) {
	for _, body := range []string{`{"status":"starting","reason":"schema"}`, `{"status":"blocked"}`, `not json`} {
		port := serveHealth(t, http.StatusServiceUnavailable, body)
		if err := awaitGatewayServing(t.Context(), "tenant", port, 1500*time.Millisecond); err == nil {
			t.Errorf("503 %q reported as serving", body)
		}
	}
}
