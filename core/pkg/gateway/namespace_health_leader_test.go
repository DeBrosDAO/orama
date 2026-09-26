package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rqliteStatusServer is an rqlited /status that, like the real one under
// -auth, answers 401 to a request without the right basic-auth credentials.
func rqliteStatusServer(t *testing.T, state string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "orama" || p != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"store":{"raft":{"state":"` + state + `"}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The probe used to be a bare GET: against an rqlited with -auth it was a 401
// on every node, read as "not the leader", so namespace repair never ran.
func TestIsRQLiteLeader_sendsTheConfiguredCredentials(t *testing.T) {
	srv := rqliteStatusServer(t, "Leader")
	hostPort := strings.TrimPrefix(srv.URL, "http://")

	for name, cfg := range map[string]Config{
		"credentials in the config": {RQLiteDSN: srv.URL, RQLiteUsername: "orama", RQLitePassword: "pw"},
		"credentials in the DSN":    {RQLiteDSN: "http://orama:pw@" + hostPort},
	} {
		t.Run(name, func(t *testing.T) {
			g := &Gateway{cfg: &cfg}
			leader, err := g.isRQLiteLeader(context.Background())
			if err != nil {
				t.Fatalf("isRQLiteLeader: %v", err)
			}
			if !leader {
				t.Fatal("the leader was reported as not the leader")
			}
		})
	}
}

func TestIsRQLiteLeader_follower(t *testing.T) {
	srv := rqliteStatusServer(t, "Follower")
	g := &Gateway{cfg: &Config{RQLiteDSN: srv.URL, RQLiteUsername: "orama", RQLitePassword: "pw"}}

	leader, err := g.isRQLiteLeader(context.Background())
	if err != nil {
		t.Fatalf("isRQLiteLeader: %v", err)
	}
	if leader {
		t.Fatal("a follower was reported as the leader")
	}
}

// A probe that cannot be answered is not a "no": it is reported, so a node
// whose credentials are wrong says so instead of silently never repairing.
func TestIsRQLiteLeader_failuresAreErrors(t *testing.T) {
	srv := rqliteStatusServer(t, "Leader")
	for name, cfg := range map[string]Config{
		"wrong credentials": {RQLiteDSN: srv.URL, RQLiteUsername: "orama", RQLitePassword: "nope"},
		"no credentials":    {RQLiteDSN: srv.URL},
		"empty DSN":         {},
		"unreachable":       {RQLiteDSN: "http://127.0.0.1:1", RQLiteUsername: "orama", RQLitePassword: "pw"},
	} {
		t.Run(name, func(t *testing.T) {
			g := &Gateway{cfg: &cfg}
			leader, err := g.isRQLiteLeader(context.Background())
			if err == nil {
				t.Fatalf("got leader=%v and no error", leader)
			}
			if leader {
				t.Error("a failed probe reported leadership")
			}
			if strings.Contains(err.Error(), "pw") || strings.Contains(err.Error(), "nope") {
				t.Errorf("the error carries the password: %v", err)
			}
		})
	}
}
