package gateway

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// TestStatusResponseWriter_extendIOReachesTheConnection: a request behind the
// status-recording wrapper (every request on the gateway is) must be able to
// outlive the server's read timeout once httputil.ExtendIO moved it.
func TestStatusResponseWriter_extendIOReachesTheConnection(t *testing.T) {
	handler := func(extend bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			srw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
			if extend {
				if err := httputil.ExtendIO(srw, httputil.TransferBudget); err != nil {
					t.Error(err)
				}
			}
			if _, err := io.ReadAll(r.Body); err != nil {
				writeError(srw, http.StatusRequestTimeout, err.Error())
				return
			}
			srw.WriteHeader(http.StatusOK)
		})
	}
	slowPost := func(t *testing.T, extend bool) int {
		srv := httptest.NewUnstartedServer(handler(extend))
		srv.Config.ReadTimeout = 200 * time.Millisecond
		srv.Start()
		defer srv.Close()
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\n\r\nab"))
		time.Sleep(500 * time.Millisecond)
		_, _ = conn.Write([]byte("cd"))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			return 0
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := slowPost(t, true); got != http.StatusOK {
		t.Fatalf("an extended request behind the wrapper answered %d, want 200", got)
	}
	if got := slowPost(t, false); got == http.StatusOK {
		t.Fatal("the control was read in full; the test proves nothing")
	}
}
