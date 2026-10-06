package ipfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestKuboAPIToken_isNotTheClusterPassword(t *testing.T) {
	token, err := KuboAPIToken("secret\n")
	if err != nil {
		t.Fatal(err)
	}
	password, err := ClusterRESTPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	if token == password || len(token) != 64 {
		t.Fatalf("token %q", token)
	}
	again, _ := KuboAPIToken("  secret")
	if again != token {
		t.Fatal("a trailing newline changed the bearer")
	}
}

func TestClient_sendsTheKuboBearerAndNotTheClusterPassword(t *testing.T) {
	token, err := KuboAPIToken("secret")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	kubo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer kubo.Close()
	c, err := NewClient(Config{IPFSAPIURL: kubo.URL, KuboAPIToken: token, ClusterAPIPassword: "not-the-token"}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.httpClient.Post(kubo.URL+"/api/v0/id", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "Bearer "+token {
		t.Fatalf("Authorization = %q", got)
	}
	ctx := context.Background()
	plain, err := PostAPI(ctx, kubo.URL+"/api/v0/id", "")
	if err != nil {
		t.Fatal(err)
	}
	plain.Body.Close()
}
