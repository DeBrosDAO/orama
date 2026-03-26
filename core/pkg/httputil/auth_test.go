package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "valid bearer token",
			header: "Bearer abc123",
			want:   "abc123",
		},
		{
			name:   "case insensitive",
			header: "bearer xyz789",
			want:   "xyz789",
		},
		{
			name:   "with extra spaces",
			header: "Bearer   token-with-spaces  ",
			want:   "token-with-spaces",
		},
		{
			name:   "no bearer scheme",
			header: "Basic abc123",
			want:   "",
		},
		{
			name:   "empty header",
			header: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}

			if got := ExtractBearerToken(req); got != tt.want {
				t.Errorf("ExtractBearerToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		header string
		xapi   string
		query  string
		want   string
	}{
		{
			name: "X-API-Key header (priority)",
			xapi: "key-from-header",
			want: "key-from-header",
		},
		{
			name:   "ApiKey scheme",
			header: "ApiKey my-api-key",
			want:   "my-api-key",
		},
		{
			name:   "Bearer with non-JWT token",
			header: "Bearer simple-token",
			want:   "simple-token",
		},
		{
			name:   "Bearer with JWT (should skip)",
			header: "Bearer eyJ.abc.xyz",
			want:   "",
		},
		{
			name:  "query parameter api_key",
			query: "?api_key=query-key",
			want:  "query-key",
		},
		{
			name:  "query parameter token",
			query: "?token=token-key",
			want:  "token-key",
		},
		{
			name:   "X-API-Key takes priority over Authorization",
			xapi:   "xapi-key",
			header: "Bearer bearer-key",
			want:   "xapi-key",
		},
		{
			name: "no auth",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/"
			if tt.query != "" {
				url += tt.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)

			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if tt.xapi != "" {
				req.Header.Set("X-API-Key", tt.xapi)
			}

			if got := ExtractAPIKey(req); got != tt.want {
				t.Errorf("ExtractAPIKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsJWT(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{
			name:  "valid JWT structure",
			token: "header.payload.signature",
			want:  true,
		},
		{
			name:  "real JWT",
			token: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			want:  true,
		},
		{
			name:  "not a JWT - no dots",
			token: "simple-token",
			want:  false,
		},
		{
			name:  "not a JWT - one dot",
			token: "part1.part2",
			want:  false,
		},
		{
			name:  "not a JWT - three dots",
			token: "a.b.c.d",
			want:  false,
		},
		{
			name:  "empty string",
			token: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsJWT(tt.token); got != tt.want {
				t.Errorf("IsJWT(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

func TestExtractNamespaceHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "valid namespace",
			header: "my-namespace",
			want:   "my-namespace",
		},
		{
			name:   "with whitespace",
			header: "  my-namespace  ",
			want:   "my-namespace",
		},
		{
			name:   "empty header",
			header: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("X-Namespace", tt.header)
			}

			if got := ExtractNamespaceHeader(req); got != tt.want {
				t.Errorf("ExtractNamespaceHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractWalletHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "valid wallet",
			header: "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEbC",
			want:   "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEbC",
		},
		{
			name:   "with whitespace",
			header: "  0x742d35Cc  ",
			want:   "0x742d35Cc",
		},
		{
			name:   "empty header",
			header: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("X-Wallet", tt.header)
			}

			if got := ExtractWalletHeader(req); got != tt.want {
				t.Errorf("ExtractWalletHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasAuthHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{
			name:   "has auth header",
			header: "Bearer token",
			want:   true,
		},
		{
			name:   "no auth header",
			header: "",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}

			if got := HasAuthHeader(req); got != tt.want {
				t.Errorf("HasAuthHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractBasicAuth(t *testing.T) {
	tests := []struct {
		name         string
		header       string
		wantUsername string
		wantPassword string
		wantOK       bool
	}{
		{
			name:         "valid basic auth",
			header:       "Basic " + basicAuth("user", "pass"),
			wantUsername: "user",
			wantPassword: "pass",
			wantOK:       true,
		},
		{
			name:   "no auth header",
			header: "",
			wantOK: false,
		},
		{
			name:   "bearer token",
			header: "Bearer token",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}

			username, password, ok := ExtractBasicAuth(req)
			if ok != tt.wantOK {
				t.Errorf("ExtractBasicAuth() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok {
				if username != tt.wantUsername {
					t.Errorf("ExtractBasicAuth() username = %v, want %v", username, tt.wantUsername)
				}
				if password != tt.wantPassword {
					t.Errorf("ExtractBasicAuth() password = %v, want %v", password, tt.wantPassword)
				}
			}
		})
	}
}

// Helper function to create basic auth header
func basicAuth(username, password string) string {
	return EncodeBase64([]byte(username + ":" + password))
}
