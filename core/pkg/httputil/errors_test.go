package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPError(t *testing.T) {
	err := NewHTTPError(http.StatusBadRequest, "invalid input")
	expected := "HTTP 400: invalid input"
	if err.Error() != expected {
		t.Errorf("HTTPError.Error() = %v, want %v", err.Error(), expected)
	}
}

func TestCheckMethod(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		expected   string
		wantResult bool
		wantStatus int
	}{
		{
			name:       "matching method",
			method:     http.MethodPost,
			expected:   http.MethodPost,
			wantResult: true,
			wantStatus: 0, // No error written
		},
		{
			name:       "non-matching method",
			method:     http.MethodGet,
			expected:   http.MethodPost,
			wantResult: false,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", nil)
			w := httptest.NewRecorder()

			result := CheckMethod(w, req, tt.expected)

			if result != tt.wantResult {
				t.Errorf("CheckMethod() = %v, want %v", result, tt.wantResult)
			}

			if tt.wantStatus > 0 && w.Code != tt.wantStatus {
				t.Errorf("CheckMethod() status = %v, want %v", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCheckMethodOneOf(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		allowed    []string
		wantResult bool
		wantStatus int
	}{
		{
			name:       "method in list",
			method:     http.MethodPost,
			allowed:    []string{http.MethodGet, http.MethodPost},
			wantResult: true,
			wantStatus: 0,
		},
		{
			name:       "method not in list",
			method:     http.MethodDelete,
			allowed:    []string{http.MethodGet, http.MethodPost},
			wantResult: false,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", nil)
			w := httptest.NewRecorder()

			result := CheckMethodOneOf(w, req, tt.allowed...)

			if result != tt.wantResult {
				t.Errorf("CheckMethodOneOf() = %v, want %v", result, tt.wantResult)
			}

			if tt.wantStatus > 0 && w.Code != tt.wantStatus {
				t.Errorf("CheckMethodOneOf() status = %v, want %v", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestRequireNotEmpty(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		fieldName  string
		wantResult bool
		wantStatus int
	}{
		{
			name:       "non-empty value",
			value:      "test",
			fieldName:  "username",
			wantResult: true,
			wantStatus: 0,
		},
		{
			name:       "empty value",
			value:      "",
			fieldName:  "username",
			wantResult: false,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "whitespace only",
			value:      "   ",
			fieldName:  "username",
			wantResult: false,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()

			result := RequireNotEmpty(w, tt.value, tt.fieldName)

			if result != tt.wantResult {
				t.Errorf("RequireNotEmpty() = %v, want %v", result, tt.wantResult)
			}

			if tt.wantStatus > 0 && w.Code != tt.wantStatus {
				t.Errorf("RequireNotEmpty() status = %v, want %v", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCommonErrors(t *testing.T) {
	tests := []struct {
		name string
		err  *HTTPError
		code int
	}{
		{"BadRequest", ErrBadRequest, http.StatusBadRequest},
		{"Unauthorized", ErrUnauthorized, http.StatusUnauthorized},
		{"Forbidden", ErrForbidden, http.StatusForbidden},
		{"NotFound", ErrNotFound, http.StatusNotFound},
		{"MethodNotAllowed", ErrMethodNotAllowed, http.StatusMethodNotAllowed},
		{"Conflict", ErrConflict, http.StatusConflict},
		{"InternalServerError", ErrInternalServerError, http.StatusInternalServerError},
		{"ServiceUnavailable", ErrServiceUnavailable, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.code {
				t.Errorf("%s.Code = %v, want %v", tt.name, tt.err.Code, tt.code)
			}
		})
	}
}

func TestWriteHTTPError(t *testing.T) {
	w := httptest.NewRecorder()
	err := NewHTTPError(http.StatusNotFound, "resource not found")
	WriteHTTPError(w, err)

	if w.Code != http.StatusNotFound {
		t.Errorf("WriteHTTPError() status = %v, want %v", w.Code, http.StatusNotFound)
	}
}
