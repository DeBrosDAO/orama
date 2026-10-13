package httputil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// serverWith runs h behind a server whose read timeout is short, the way the
// gateways' 60s one is, and returns the status of a POST whose body takes
// bodyTime to arrive.
func serverWith(t *testing.T, readTimeout, bodyTime time.Duration, h http.HandlerFunc) (int, error) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadTimeout = readTimeout
	srv.Start()
	t.Cleanup(srv.Close)
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("first"))
		time.Sleep(bodyTime)
		_, _ = pw.Write([]byte("last"))
		pw.Close()
	}()
	resp, err := http.Post(srv.URL, "application/octet-stream", pr)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func readAll(w http.ResponseWriter, r *http.Request) {
	if _, err := io.ReadAll(r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusRequestTimeout)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func TestExtendIO_aSlowBodyOutlivesTheServersReadTimeout(t *testing.T) {
	status, err := serverWith(t, 200*time.Millisecond, 600*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		if err := ExtendIO(w, TransferBudget); err != nil {
			t.Error(err)
		}
		readAll(w, r)
	})
	if err != nil || status != http.StatusOK {
		t.Fatalf("status %d, %v: the extended request was cut off", status, err)
	}
}

func TestExtendIO_withoutItTheServersReadTimeoutStillApplies(t *testing.T) {
	status, err := serverWith(t, 200*time.Millisecond, 600*time.Millisecond, readAll)
	if err == nil && status == http.StatusOK {
		t.Fatal("a body slower than the read timeout was read in full; the control proves nothing")
	}
}

func TestExtendIO_aWriterWithNoDeadlinesIsNotAnError(t *testing.T) {
	if err := ExtendIO(httptest.NewRecorder(), time.Second); err != nil {
		t.Fatal(err)
	}
}
