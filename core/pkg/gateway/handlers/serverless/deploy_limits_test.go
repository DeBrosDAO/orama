package serverless

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// limitsDeployRequest is a multipart deploy carrying the given form fields.
func limitsDeployRequest(t *testing.T, metadata string, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("name", "hello")
	if metadata != "" {
		_ = mw.WriteField("metadata", metadata)
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	part, err := mw.CreateFormFile("wasm", "function.wasm")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = part.Write([]byte("\x00asm"))
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/functions", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return asCredentialOf(req, "tenant")
}

func TestDeployFunction_refusesInvalidLimits(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		fields   map[string]string
		wantName string
	}{
		{"memory above the maximum", "", map[string]string{"memory_limit_mb": "4096"}, "memory_limit_mb"},
		{"timeout above the maximum", "", map[string]string{"timeout_seconds": "100000"}, "timeout_seconds"},
		{"memory malformed", "", map[string]string{"memory_limit_mb": "lots"}, "memory_limit_mb"},
		{"timeout malformed", "", map[string]string{"timeout_seconds": "1.5"}, "timeout_seconds"},
		{"memory zero", "", map[string]string{"memory_limit_mb": "0"}, "memory_limit_mb"},
		{"timeout negative", "", map[string]string{"timeout_seconds": "-3"}, "timeout_seconds"},
		{"retry count malformed", "", map[string]string{"retry_count": "x"}, "retry_count"},
		{"retry delay negative", "", map[string]string{"retry_delay_seconds": "-1"}, "retry_delay_seconds"},
		{"metadata memory above the maximum", `{"memory_limit_mb":4096}`, nil, "memory_limit_mb"},
		{"metadata timeout negative", `{"timeout_seconds":-1}`, nil, "timeout_seconds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &recordingRegistry{mockRegistry: newMockRegistry()}
			h := newTestHandlers(reg)
			rec := httptest.NewRecorder()

			h.DeployFunction(rec, limitsDeployRequest(t, tt.metadata, tt.fields))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, "VALIDATION_FAILED") || !strings.Contains(body, tt.wantName) {
				t.Errorf("body %s does not name VALIDATION_FAILED and %s", body, tt.wantName)
			}
			if len(reg.registered) != 0 {
				t.Errorf("a refused deploy reached the registry: %+v", reg.registered[0])
			}
		})
	}
}

func TestDeployFunction_acceptsLimitsInRangeAndAbsent(t *testing.T) {
	tests := []struct {
		name       string
		fields     map[string]string
		wantMemory int
		wantTime   int
	}{
		{"absent keeps the registry defaults", nil, 0, 0},
		{"at the maximum", map[string]string{"memory_limit_mb": "256", "timeout_seconds": "300"}, 256, 300},
		{"at the minimum", map[string]string{"memory_limit_mb": "1", "timeout_seconds": "1"}, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &recordingRegistry{mockRegistry: newMockRegistry()}
			h := newTestHandlers(reg)
			rec := httptest.NewRecorder()

			h.DeployFunction(rec, limitsDeployRequest(t, "", tt.fields))

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
			def := reg.registered[0]
			if def.MemoryLimitMB != tt.wantMemory || def.TimeoutSeconds != tt.wantTime {
				t.Errorf("registered memory=%d timeout=%d, want %d/%d", def.MemoryLimitMB, def.TimeoutSeconds, tt.wantMemory, tt.wantTime)
			}
		})
	}
}

// TestDeployFunction_acceptsZeroRetries: "never retry" is a retry count of 0,
// the default; a definition that states it must deploy (the reference apps'
// function.yaml does, and every one of their deploys was refused).
func TestDeployFunction_acceptsZeroRetries(t *testing.T) {
	reg := &recordingRegistry{mockRegistry: newMockRegistry()}
	h := newTestHandlers(reg)
	rec := httptest.NewRecorder()

	h.DeployFunction(rec, limitsDeployRequest(t, "", map[string]string{"retry_count": "0", "retry_delay_seconds": "0"}))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if def := reg.registered[0]; def.RetryCount != 0 || def.RetryDelaySeconds != 0 {
		t.Errorf("registered retry=%d delay=%d, want 0/0", def.RetryCount, def.RetryDelaySeconds)
	}
}

func TestDeployFunction_refusesNegativeRetries(t *testing.T) {
	reg := &recordingRegistry{mockRegistry: newMockRegistry()}
	h := newTestHandlers(reg)
	rec := httptest.NewRecorder()

	h.DeployFunction(rec, limitsDeployRequest(t, "", map[string]string{"retry_count": "-1"}))

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "retry_count") {
		t.Fatalf("status = %d, want 400 naming retry_count: %s", rec.Code, rec.Body.String())
	}
}

func TestDeployFunction_usesConfiguredMaxima(t *testing.T) {
	reg := &recordingRegistry{mockRegistry: newMockRegistry()}
	h := newTestHandlers(reg)
	h.SetFunctionLimits(128, 60, 5)

	rec := httptest.NewRecorder()
	h.DeployFunction(rec, limitsDeployRequest(t, "", map[string]string{"timeout_seconds": "61"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("timeout 61 over a 60 s maximum: status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.DeployFunction(rec, limitsDeployRequest(t, "", map[string]string{"memory_limit_mb": "129"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("memory 129 over a 128 MB maximum: status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.DeployFunction(rec, limitsDeployRequest(t, `{"retry_count":6}`, nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "retry_count") {
		t.Fatalf("retry_count 6 over a maximum of 5: status = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.DeployFunction(rec, limitsDeployRequest(t, "", map[string]string{"retry_count": "5"}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("retry_count at the maximum: status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}
