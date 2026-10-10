package ipfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// bugboard #414: InPinset reads the cluster's pinset (GET /allocations/<cid>),
// which answers 404 for a CID the cluster does not hold.
func TestInPinset(t *testing.T) {
	const cid = "QmfYzxZHqYpmy29rVWqs6f4igzYngACaxSxPWdf7FspuDV"
	for _, tc := range []struct {
		name    string
		status  int
		want    bool
		wantErr bool
	}{
		{"pinned", http.StatusOK, true, false},
		{"not in the pinset", http.StatusNotFound, false, false},
		{"a 404 that is not about the pinset", http.StatusNotFound, false, true},
		{"cluster error", http.StatusInternalServerError, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(tc.status)
				body := `{}`
				if tc.name == "not in the pinset" {
					body = `{"code":404,"message":"pin is not part of the pinset"}` // live devnet answer
				}
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			c, err := NewClient(Config{ClusterAPIURL: srv.URL}, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}

			got, err := c.InPinset(context.Background(), cid)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("InPinset = %v, %v; want %v, error=%v", got, err, tc.want, tc.wantErr)
			}
			if gotPath != "/allocations/"+cid {
				t.Errorf("asked %s, want the cluster's /allocations/<cid>", gotPath)
			}
		})
	}
}

func TestInPinset_clusterDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c, err := NewClient(Config{ClusterAPIURL: srv.URL}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.InPinset(context.Background(), "QmfYzxZHqYpmy29rVWqs6f4igzYngACaxSxPWdf7FspuDV"); err == nil {
		t.Fatal("InPinset against a dead cluster returned no error")
	}
}
