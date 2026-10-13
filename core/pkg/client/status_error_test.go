package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	orerrors "github.com/DeBrosOfficial/network/pkg/errors"
)

// storageAnswering is a storage client whose gateway answers every request
// with status and body.
func storageAnswering(t *testing.T, status int, body string) *StorageClientImpl {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return &StorageClientImpl{client: &Client{config: &ClientConfig{GatewayURL: server.URL, AppName: "test-app", APIKey: "ak_test:test-app"}}}
}

// The documented helpers classify a storage failure by its status. Storage
// used to return the status as text, so errors.IsUnauthorized said false for a
// 401 and an application could not tell a bad credential from an outage.
func TestStorageClient_errorsAreTypedByStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		matches func(error) bool
	}{
		"401 is unauthorized": {http.StatusUnauthorized, orerrors.IsUnauthorized},
		"403 is forbidden":    {http.StatusForbidden, orerrors.IsForbidden},
		"404 is not found":    {http.StatusNotFound, orerrors.IsNotFound},
		"409 is a conflict":   {http.StatusConflict, orerrors.IsConflict},
		"400 is validation":   {http.StatusBadRequest, orerrors.IsValidation},
	} {
		t.Run(name, func(t *testing.T) {
			s := storageAnswering(t, tc.status, `{"error":"refused for the test"}`)
			calls := map[string]error{}
			_, calls["upload"] = s.Upload(context.Background(), strings.NewReader("x"), "x.txt")
			_, calls["pin"] = s.Pin(context.Background(), "bafy", "x")
			_, calls["status"] = s.Status(context.Background(), "bafy")
			_, calls["get"] = s.Get(context.Background(), "bafy")
			calls["unpin"] = s.Unpin(context.Background(), "bafy")
			for op, err := range calls {
				if err == nil || !tc.matches(err) {
					t.Errorf("%s: %v is not classified", op, err)
				}
				if err != nil && !strings.Contains(err.Error(), "refused for the test") {
					t.Errorf("%s: %q lost the gateway's message", op, err)
				}
			}
		})
	}
}

// A status no helper names is classified as none of them.
func TestStorageClient_aServerErrorMatchesNoHelper(t *testing.T) {
	_, err := storageAnswering(t, http.StatusInternalServerError, "boom").Upload(context.Background(), strings.NewReader("x"), "x.txt")
	if err == nil {
		t.Fatal("a 500 was not an error")
	}
	for name, is := range map[string]func(error) bool{
		"unauthorized": orerrors.IsUnauthorized, "forbidden": orerrors.IsForbidden,
		"not found": orerrors.IsNotFound, "validation": orerrors.IsValidation,
	} {
		if is(err) {
			t.Errorf("a 500 is classified as %s", name)
		}
	}
}

// The client refuses before any request when it holds no credential, and that
// refusal is an authentication failure too.
func TestStorageClient_noCredentialIsUnauthorized(t *testing.T) {
	s := &StorageClientImpl{client: &Client{config: &ClientConfig{GatewayURL: "http://127.0.0.1:1", AppName: "test-app"}}}
	if _, err := s.Upload(context.Background(), strings.NewReader("x"), "x.txt"); !orerrors.IsUnauthorized(err) {
		t.Errorf("upload with no credential: %v is not unauthorized", err)
	}
}
