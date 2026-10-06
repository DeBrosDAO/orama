package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveNoStore(t *testing.T, path string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	g := &Gateway{cfg: &Config{}}
	rr := httptest.NewRecorder()
	g.securityHeadersMiddleware(h).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

func TestNoStoreAPIResponses_storage_get_is_no_store(t *testing.T) {
	rr := serveNoStore(t, "/v1/storage/get/bafyabc", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment")
		_, _ = w.Write([]byte("blob"))
	})
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rr.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
}

func TestNoStoreAPIResponses_json_route_is_no_store(t *testing.T) {
	rr := serveNoStore(t, "/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestNoStoreAPIResponses_error_responses_are_no_store(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError} {
		rr := serveNoStore(t, "/v1/anything", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, code, "nope")
		})
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("status %d: Cache-Control = %q, want no-store", code, got)
		}
	}
}

func TestNoStoreAPIResponses_handler_chosen_value_is_kept(t *testing.T) {
	rr := serveNoStore(t, "/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=5")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	if got := rr.Header().Values("Cache-Control"); len(got) != 1 || got[0] != "public, max-age=5" {
		t.Fatalf("Cache-Control = %v, want [public, max-age=5]", got)
	}
	if got := rr.Header().Get("Pragma"); got != "" {
		t.Fatalf("Pragma = %q, want none beside a handler-chosen Cache-Control", got)
	}
}

func TestNoStoreAPIResponses_proxied_upstream_value_is_not_doubled(t *testing.T) {
	// The reverse-proxy paths copy upstream headers with Add.
	rr := serveNoStore(t, "/v1/anything", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Cache-Control", "private, max-age=60")
		w.WriteHeader(http.StatusOK)
	})
	if got := rr.Header().Values("Cache-Control"); len(got) != 1 || got[0] != "private, max-age=60" {
		t.Fatalf("Cache-Control = %v, want the upstream value alone", got)
	}
}

func TestNoStoreAPIResponses_static_deployment_path_untouched(t *testing.T) {
	rr := serveNoStore(t, "/assets/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write([]byte("js"))
	})
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("Cache-Control = %q, want the deployment's own", got)
	}
}

func TestNoStoreAPIResponses_non_api_path_without_value_stays_unset(t *testing.T) {
	rr := serveNoStore(t, "/index.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>"))
	})
	if got := rr.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("Cache-Control = %q, want unset outside /v1/", got)
	}
}

func TestNoStoreAPIResponses_interim_1xx_does_not_consume_default(t *testing.T) {
	rr := serveNoStore(t, "/v1/anything", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusOK)
	})
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}
