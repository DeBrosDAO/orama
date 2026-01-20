package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		data       any
		wantStatus int
		wantBody   string
	}{
		{
			name:       "simple map",
			code:       http.StatusOK,
			data:       map[string]any{"key": "value"},
			wantStatus: http.StatusOK,
			wantBody:   `{"key":"value"}`,
		},
		{
			name:       "array",
			code:       http.StatusCreated,
			data:       []string{"a", "b", "c"},
			wantStatus: http.StatusCreated,
			wantBody:   `["a","b","c"]`,
		},
		{
			name:       "nested structure",
			code:       http.StatusOK,
			data:       map[string]any{"user": map[string]any{"name": "Alice", "age": 30}},
			wantStatus: http.StatusOK,
			wantBody:   `{"user":{"age":30,"name":"Alice"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteJSON(w, tt.code, tt.data)

			if w.Code != tt.wantStatus {
				t.Errorf("WriteJSON() status = %v, want %v", w.Code, tt.wantStatus)
			}

			if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
				t.Errorf("WriteJSON() Content-Type = %v, want application/json", contentType)
			}

			var got, want any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}
			if err := json.Unmarshal([]byte(tt.wantBody), &want); err != nil {
				t.Fatalf("failed to unmarshal expected: %v", err)
			}

			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("WriteJSON() body = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		message    string
		wantStatus int
	}{
		{
			name:       "bad request",
			code:       http.StatusBadRequest,
			message:    "invalid input",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unauthorized",
			code:       http.StatusUnauthorized,
			message:    "missing credentials",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "internal error",
			code:       http.StatusInternalServerError,
			message:    "something went wrong",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteError(w, tt.code, tt.message)

			if w.Code != tt.wantStatus {
				t.Errorf("WriteError() status = %v, want %v", w.Code, tt.wantStatus)
			}

			var response map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if msg, ok := response["error"].(string); !ok || msg != tt.message {
				t.Errorf("WriteError() message = %v, want %v", msg, tt.message)
			}
		})
	}
}

func TestWriteSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	WriteSuccess(w)

	if w.Code != http.StatusOK {
		t.Errorf("WriteSuccess() status = %v, want %v", w.Code, http.StatusOK)
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if status, ok := response["status"].(string); !ok || status != "ok" {
		t.Errorf("WriteSuccess() status = %v, want ok", status)
	}
}

func TestWriteSuccessWithData(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]any{
		"user_id": "123",
		"name":    "Alice",
	}
	WriteSuccessWithData(w, data)

	if w.Code != http.StatusOK {
		t.Errorf("WriteSuccessWithData() status = %v, want %v", w.Code, http.StatusOK)
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if status, ok := response["status"].(string); !ok || status != "ok" {
		t.Errorf("WriteSuccessWithData() status = %v, want ok", status)
	}

	if userID, ok := response["user_id"].(string); !ok || userID != "123" {
		t.Errorf("WriteSuccessWithData() user_id = %v, want 123", userID)
	}

	if name, ok := response["name"].(string); !ok || name != "Alice" {
		t.Errorf("WriteSuccessWithData() name = %v, want Alice", name)
	}
}
