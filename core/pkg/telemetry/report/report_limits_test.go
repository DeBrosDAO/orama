package report

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// endlessBody answers with more than the cap, as a tenant process squatting
// a stopped service's loopback port could.
func endlessBody(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for written := 0; written <= maxLocalResponseBytes; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReadLocalBody_atTheCapIsRead(t *testing.T) {
	body, err := readLocalBody(bytes.NewReader(make([]byte, maxLocalResponseBytes)), "http://127.0.0.1:1/x")
	if err != nil || len(body) != maxLocalResponseBytes {
		t.Fatalf("len %d, err %v; a body exactly at the cap must be read", len(body), err)
	}
}

func TestReadLocalBody_overTheCapIsAnError(t *testing.T) {
	_, err := readLocalBody(bytes.NewReader(make([]byte, maxLocalResponseBytes+1)), "http://127.0.0.1:1/x")
	if err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:1/x") || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v; want an error naming the URL and the limit", err)
	}
}

func TestReadLocalBody_empty(t *testing.T) {
	body, err := readLocalBody(strings.NewReader(""), "http://127.0.0.1:1/x")
	if err != nil || len(body) != 0 {
		t.Fatalf("body %q, err %v", body, err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReadLocalBody_readErrorNamesTheURL(t *testing.T) {
	_, err := readLocalBody(failingReader{}, "http://127.0.0.1:1/x")
	if err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:1/x") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPGet_oversizedBodyIsRefused(t *testing.T) {
	srv := endlessBody(t)
	body, err := httpGet(context.Background(), srv.URL+"/status")
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("read %d bytes, err %v; want the cap to refuse the body", len(body), err)
	}
	if body != nil {
		t.Errorf("returned %d bytes of an oversized body", len(body))
	}
}

// An oversized health body is refused even when it is valid JSON the
// gateway's parser would accept.
func TestGatewayStatus_oversizedHealthBodyKeepsNoSubsystems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"checks":{"rqlite":{"status":"ok"}},"pad":"%s"}`, strings.Repeat("x", maxLocalResponseBytes))
	}))
	t.Cleanup(srv.Close)
	r := gatewayStatus(context.Background(), srv.URL)
	if len(r.Subsystems) != 0 || r.Version != "" {
		t.Errorf("an oversized body was parsed: %+v", r)
	}
}

// clusterGet reads the cluster REST API's port, which a tenant process can
// bind while ipfs-cluster is stopped.
func TestClusterGet_oversizedBodyIsRefused(t *testing.T) {
	srv := endlessBody(t)
	srv.Close()
	ln, err := net.Listen("tcp", constants.LocalIPFSClusterURL()[len("http://"):])
	if err != nil {
		t.Skipf("the cluster API port is in use on this machine: %v", err)
	}
	squatter := httptest.NewUnstartedServer(srv.Config.Handler)
	squatter.Listener = ln
	squatter.Start()
	defer squatter.Close()

	if _, err := clusterGet(context.Background(), "/id", "pw"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v; want the cap to refuse the endless body", err)
	}
}

// runCmd must stop a hung command at its deadline rather than wait for it.
func TestRunCmd_hungCommandIsKilledAtTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runCmd(ctx, "sleep", "10"); err == nil {
		t.Fatal("a killed command reported success")
	}
	if elapsed := time.Since(start); elapsed > localCommandTimeout {
		t.Errorf("runCmd waited %v for a hung command", elapsed)
	}
}
