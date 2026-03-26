package httputil

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "valid json",
			body:    `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "invalid json",
			body:    `{invalid}`,
			wantErr: true,
		},
		{
			name:    "empty object",
			body:    `{}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tt.body))
			var result map[string]any
			err := DecodeJSON(req, &result)

			if (err != nil) != tt.wantErr {
				t.Errorf("DecodeJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeBase64(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "valid base64",
			input:   "SGVsbG8gV29ybGQ=",
			want:    "Hello World",
			wantErr: false,
		},
		{
			name:    "invalid base64",
			input:   "not-base64!@#",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeBase64(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("DecodeBase64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && string(got) != tt.want {
				t.Errorf("DecodeBase64() = %v, want %v", string(got), tt.want)
			}
		})
	}
}

func TestEncodeBase64(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{
			name:  "simple string",
			input: []byte("Hello World"),
			want:  "SGVsbG8gV29ybGQ=",
		},
		{
			name:  "empty bytes",
			input: []byte{},
			want:  "",
		},
		{
			name:  "binary data",
			input: []byte{0, 1, 2, 3, 4},
			want:  "AAECAwQ=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeBase64(tt.input); got != tt.want {
				t.Errorf("EncodeBase64() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryParam(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		key          string
		defaultValue string
		want         string
	}{
		{
			name:         "param exists",
			url:          "http://example.com?key=value",
			key:          "key",
			defaultValue: "default",
			want:         "value",
		},
		{
			name:         "param missing",
			url:          "http://example.com",
			key:          "key",
			defaultValue: "default",
			want:         "default",
		},
		{
			name:         "empty param",
			url:          "http://example.com?key=",
			key:          "key",
			defaultValue: "default",
			want:         "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			if got := QueryParam(req, tt.key, tt.defaultValue); got != tt.want {
				t.Errorf("QueryParam() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryParamInt(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		key          string
		defaultValue int
		want         int
	}{
		{
			name:         "valid integer",
			url:          "http://example.com?page=5",
			key:          "page",
			defaultValue: 1,
			want:         5,
		},
		{
			name:         "invalid integer",
			url:          "http://example.com?page=abc",
			key:          "page",
			defaultValue: 1,
			want:         1,
		},
		{
			name:         "missing param",
			url:          "http://example.com",
			key:          "page",
			defaultValue: 1,
			want:         1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			if got := QueryParamInt(req, tt.key, tt.defaultValue); got != tt.want {
				t.Errorf("QueryParamInt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryParamBool(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		key          string
		defaultValue bool
		want         bool
	}{
		{
			name:         "true value",
			url:          "http://example.com?enabled=true",
			key:          "enabled",
			defaultValue: false,
			want:         true,
		},
		{
			name:         "false value",
			url:          "http://example.com?enabled=false",
			key:          "enabled",
			defaultValue: true,
			want:         false,
		},
		{
			name:         "1 value",
			url:          "http://example.com?enabled=1",
			key:          "enabled",
			defaultValue: false,
			want:         true,
		},
		{
			name:         "missing param",
			url:          "http://example.com",
			key:          "enabled",
			defaultValue: true,
			want:         true,
		},
		{
			name:         "invalid value",
			url:          "http://example.com?enabled=maybe",
			key:          "enabled",
			defaultValue: false,
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			if got := QueryParamBool(req, tt.key, tt.defaultValue); got != tt.want {
				t.Errorf("QueryParamBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadBody(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		maxBytes int64
		want     string
	}{
		{
			name:     "normal read",
			body:     "Hello World",
			maxBytes: 1024,
			want:     "Hello World",
		},
		{
			name:     "truncated read",
			body:     "Hello World",
			maxBytes: 5,
			want:     "Hello",
		},
		{
			name:     "empty body",
			body:     "",
			maxBytes: 1024,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tt.body))
			got, err := ReadBody(req, tt.maxBytes)
			if err != nil {
				t.Errorf("ReadBody() error = %v", err)
				return
			}
			if string(got) != tt.want {
				t.Errorf("ReadBody() = %v, want %v", string(got), tt.want)
			}
		})
	}
}
