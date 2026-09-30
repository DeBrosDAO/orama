package clusterreg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

const testPoll = time.Millisecond

var testHash = strings.Repeat("AB", 32)

// txServer answers 404 for the first misses lookups, then answer.
func txServer(t *testing.T, misses int32, status int, answer string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/cosmos/tx/v1beta1/txs/"+testHash) {
			t.Errorf("path %s", r.URL.Path)
		}
		if calls.Add(1) <= misses {
			http.Error(w, `{"code":5,"message":"tx not found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestWaitIncluded_waitsForTheBlock(t *testing.T) {
	srv, calls := txServer(t, 3, http.StatusOK, `{"tx_response":{"height":"120","code":0}}`)
	height, err := WaitIncluded(context.Background(), srv.URL, testHash, time.Minute, testPoll)
	if err != nil || height != 120 {
		t.Fatalf("height %d err %v", height, err)
	}
	if calls.Load() != 4 {
		t.Errorf("%d lookups, want 4 (3 not found, then the block)", calls.Load())
	}
}

// Admission to the mempool is not success: a transaction that fails in its block is an error that
// carries the chain's log.
func TestWaitIncluded_aTransactionThatFailsInItsBlockIsAnError(t *testing.T) {
	srv, _ := txServer(t, 0, http.StatusOK, `{"tx_response":{"height":"7","code":11,"raw_log":"out of gas"}}`)
	_, err := WaitIncluded(context.Background(), srv.URL, testHash, time.Minute, testPoll)
	if err == nil || !strings.Contains(err.Error(), "block 7 (code 11): out of gas") {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitIncluded_notInABlockByTheDeadlineIsReported(t *testing.T) {
	srv, _ := txServer(t, 1<<30, http.StatusOK, "")
	_, err := WaitIncluded(context.Background(), srv.URL, testHash, 20*time.Millisecond, testPoll)
	if !errors.Is(err, ErrNotIncluded) || !strings.Contains(err.Error(), testHash) {
		t.Fatalf("err = %v, want ErrNotIncluded naming the hash", err)
	}
}

// Only "not found" means "not yet": any other failure ends the wait at once.
func TestWaitIncluded_anotherErrorEndsTheWait(t *testing.T) {
	srv, calls := txServer(t, 0, http.StatusInternalServerError, "boom")
	_, err := WaitIncluded(context.Background(), srv.URL, testHash, time.Minute, testPoll)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || calls.Load() != 1 {
		t.Fatalf("err = %v after %d lookups", err, calls.Load())
	}
}

func TestWaitIncluded_aResultWithNoHeightIsAnError(t *testing.T) {
	for _, body := range []string{`{"tx_response":{"code":0}}`, `not json`, `{"tx_response":{"height":"0"}}`} {
		srv, _ := txServer(t, 0, http.StatusOK, body)
		if _, err := WaitIncluded(context.Background(), srv.URL, testHash, time.Minute, testPoll); err == nil {
			t.Errorf("%s: no error", body)
		}
	}
}

// A caller that cancels gets its cancellation back, not a timeout.
func TestWaitIncluded_theCallersCancellationIsReturned(t *testing.T) {
	srv, _ := txServer(t, 1<<30, http.StatusOK, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WaitIncluded(ctx, srv.URL, testHash, time.Minute, testPoll); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// The hash goes into the lookup's URL path and comes from the node: anything but a hex hash is refused.
func TestWaitIncluded_refusesAHashThatIsNotHex(t *testing.T) {
	srv, calls := txServer(t, 0, http.StatusOK, `{"tx_response":{"height":"1","code":0}}`)
	for _, hash := range []string{"", "ABC", testHash + "/../x", strings.Repeat("zz", 32), "?q=" + testHash} {
		if _, err := WaitIncluded(context.Background(), srv.URL, hash, time.Minute, testPoll); err == nil {
			t.Errorf("%q: no error", hash)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("%d lookups were made with a bad hash", calls.Load())
	}
}

// A hostile node's log cannot put terminal escapes on the operator's screen.
func TestWaitIncluded_theChainsLogLosesControlCharacters(t *testing.T) {
	srv, _ := txServer(t, 0, http.StatusOK, `{"tx_response":{"height":"7","code":3,"raw_log":"bad\u001b[2Jthing\n"}}`)
	_, err := WaitIncluded(context.Background(), srv.URL, testHash, time.Minute, testPoll)
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\n") || !strings.Contains(err.Error(), "bad[2Jthing") {
		t.Fatalf("err = %q", err)
	}
}

// The quoted log is cut on a character, never inside one.
func TestWaitIncluded_aLongLogIsCutOnACharacter(t *testing.T) {
	long := strings.Repeat("é", maxResultLog+10)
	srv, _ := txServer(t, 0, http.StatusOK, `{"tx_response":{"height":"7","code":3,"raw_log":"`+long+`"}}`)
	_, err := WaitIncluded(context.Background(), srv.URL, testHash, time.Minute, testPoll)
	if err == nil || !utf8.ValidString(err.Error()) || !strings.Contains(err.Error(), strings.Repeat("é", maxResultLog)) {
		t.Fatalf("err = %q", err)
	}
}
