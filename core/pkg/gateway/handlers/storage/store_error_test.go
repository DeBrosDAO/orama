package storage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validCID = "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG"

// A namespace cluster without a leader answered every storage call with 500
// "failed to verify access" (stagenet, sdk-go Storage().Get).
func TestStorageHandlers_databaseWithoutLeaderIs503(t *testing.T) {
	calls := map[string]func(h *Handlers) *httptest.ResponseRecorder{
		"get": func(h *Handlers) *httptest.ResponseRecorder {
			return serve(h.DownloadHandler, http.MethodGet, "/v1/storage/get/"+validCID, "")
		},
		"status": func(h *Handlers) *httptest.ResponseRecorder {
			return serve(h.StatusHandler, http.MethodGet, "/v1/storage/status/"+validCID, "")
		},
		"pin": func(h *Handlers) *httptest.ResponseRecorder {
			return serve(h.PinHandler, http.MethodPost, "/v1/storage/pin", `{"cid":"`+validCID+`"}`)
		},
		"unpin": func(h *Handlers) *httptest.ResponseRecorder {
			return serve(h.UnpinHandler, http.MethodDelete, "/v1/storage/unpin/"+validCID, "")
		},
	}
	for name, call := range calls {
		for _, cause := range []string{"leader not found", "not leader", "context deadline exceeded", "connection refused"} {
			t.Run(name+"/"+cause, func(t *testing.T) {
				h := newHandlersWithDB(&mockIPFSClient{}, &mockStorageDB{queryErr: errors.New("rqlite: " + cause)})
				rec := call(h)
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("status %d (%s), want 503", rec.Code, rec.Body)
				}
				if !strings.Contains(rec.Body.String(), `"retryable":true`) {
					t.Errorf("503 is not marked retryable: %s", rec.Body)
				}
			})
		}
	}
}

func TestStorageHandlers_otherDatabaseFaultStaysAnOpaque500(t *testing.T) {
	h := newHandlersWithDB(&mockIPFSClient{}, &mockStorageDB{queryErr: errors.New("no such table: ipfs_content_ownership")})
	rec := serve(h.DownloadHandler, http.MethodGet, "/v1/storage/get/"+validCID, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "no such table") {
		t.Errorf("driver text leaked: %s", rec.Body)
	}
}

// GET /v1/storage/get/%00 was a bare 500 from the proxy; the handler itself
// refuses a CID that does not parse, NUL included, before any lookup.
func TestDownloadHandler_controlCharacterCIDIs400(t *testing.T) {
	h := newHandlersWithDB(&mockIPFSClient{}, &mockStorageDB{queryErr: errors.New("must not be reached")})
	for _, raw := range []string{"%00", "Qm%20x", "%0a", validCID + "%00"} {
		rec := serve(h.DownloadHandler, http.MethodGet, "/v1/storage/get/"+raw, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %q: status %d (%s), want 400", raw, rec.Code, rec.Body)
		}
	}
}

func serve(handler http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	req := withNamespace(httptest.NewRequest(method, path, strings.NewReader(body)), "test-ns")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}
