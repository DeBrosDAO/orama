package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractAPIKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer ak_foo:ns")
	if got := extractAPIKey(r); got != "ak_foo:ns" {
		t.Fatalf("got %q", got)
	}
	r.Header.Set("Authorization", "ApiKey ak2")
	if got := extractAPIKey(r); got != "ak2" {
		t.Fatalf("got %q", got)
	}
	r.Header.Set("Authorization", "ak3raw")
	if got := extractAPIKey(r); got != "ak3raw" {
		t.Fatalf("got %q", got)
	}
	r.Header = http.Header{}
	r.Header.Set("X-API-Key", "xkey")
	if got := extractAPIKey(r); got != "xkey" {
		t.Fatalf("got %q", got)
	}
}

// TestDomainRoutingMiddleware_NonDebrosNetwork tests that non-debros domains pass through
func TestDomainRoutingMiddleware_NonDebrosNetwork(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	g := &Gateway{}
	middleware := g.domainRoutingMiddleware(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com"

	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("Expected next handler to be called for non-debros domain")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

// TestDomainRoutingMiddleware_APIPathBypass tests that /v1/ paths bypass routing
func TestDomainRoutingMiddleware_APIPathBypass(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	g := &Gateway{}
	middleware := g.domainRoutingMiddleware(next)

	req := httptest.NewRequest("GET", "/v1/deployments/list", nil)
	req.Host = "myapp.orama.network"

	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("Expected next handler to be called for /v1/ path")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

// TestDomainRoutingMiddleware_WellKnownBypass tests that /.well-known/ paths bypass routing
func TestDomainRoutingMiddleware_WellKnownBypass(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	g := &Gateway{}
	middleware := g.domainRoutingMiddleware(next)

	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/test", nil)
	req.Host = "myapp.orama.network"

	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("Expected next handler to be called for /.well-known/ path")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

// TestDomainRoutingMiddleware_NoDeploymentService tests graceful handling when deployment service is nil
func TestDomainRoutingMiddleware_NoDeploymentService(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	g := &Gateway{
		// deploymentService is nil
		staticHandler: nil,
	}
	middleware := g.domainRoutingMiddleware(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "myapp.orama.network"

	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("Expected next handler to be called when deployment service is nil")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}
