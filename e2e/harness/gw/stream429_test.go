package gw

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/pace"
)

// TestStream_pacedTooManyRequestsIsAPacingError: a paced stream to a
// credential route answered 429 fails with *PacingError, as Do does,
// instead of handing the test a stream.
func TestStream_pacedTooManyRequestsIsAPacingError(t *testing.T) {
	c, _ := streamClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	p, err := pace.New(filepath.Join(t.TempDir(), pace.FileName), pace.DefaultBudgets())
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.WithPacer(p).Stream(context.Background(), Req{Method: http.MethodPost, Path: PathDeviceToken})
	var pe *PacingError
	if s != nil || !errors.As(err, &pe) {
		t.Fatalf("stream %v err %v", s, err)
	}
	// Unpaced, the 429 is a plain response the test asserts on.
	s, err = c.WithPacer(p).Unpaced().Stream(context.Background(), Req{Method: http.MethodPost, Path: PathDeviceToken})
	if err != nil || s.Status != http.StatusTooManyRequests {
		t.Fatalf("unpaced: %v %v", s, err)
	}
	_ = s.Close()
}

func TestNewWithTLS_boundsIdleConnections(t *testing.T) {
	c, err := NewWithTLS("http://127.0.0.1:1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := c.HTTP.Transport.(*http.Transport)
	if tr.IdleConnTimeout <= 0 || tr.MaxIdleConnsPerHost <= 0 || tr.TLSHandshakeTimeout <= 0 {
		t.Fatalf("transport %+v keeps idle connections unbounded", tr)
	}
}
