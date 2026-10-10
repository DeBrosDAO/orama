package node

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
)

func gcEnv(base string) func(string) string {
	env := map[string]string{ipfsGCAPIEnv: base, ipfsGCAuthEnv: "bearer:tok"}
	return func(k string) string { return env[k] }
}

const gcAPI = "/ip4/127.0.0.1/tcp/10102"

func TestRunIPFSGC_reportsWhatItRemoved(t *testing.T) {
	var gotURL, gotToken string
	var out bytes.Buffer
	err := runIPFSGC(context.Background(), &out, gcEnv(gcAPI), func(_ context.Context, url, token string) (int, error) {
		gotURL, gotToken = url, token
		return 7, nil
	})
	if err != nil || !strings.Contains(out.String(), "removed 7 blocks") {
		t.Fatalf("runIPFSGC = %v, output %q", err, out.String())
	}
	if gotURL != "http://127.0.0.1:10102" || gotToken != "tok" {
		t.Errorf("collected through %q with %q", gotURL, gotToken)
	}
}

// The reason the command exists: a unit stopped mid-collection (orama node
// restart, an upgrade) ended failed.
func TestRunIPFSGC_stopWhileCollectingIsNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	err := runIPFSGC(ctx, &out, gcEnv(gcAPI), func(ctx context.Context, _, _ string) (int, error) {
		cancel()
		<-ctx.Done()
		return 3, ctx.Err()
	})
	if err != nil {
		t.Fatalf("a collection stopped on request was reported as a failure: %v", err)
	}
	if !strings.Contains(out.String(), "after 3 blocks") {
		t.Errorf("output = %q, want the stop and the blocks removed so far", out.String())
	}
}

func TestRunIPFSGC_failureIsAFailure(t *testing.T) {
	errDaemon := errors.New("connection refused")
	err := runIPFSGC(context.Background(), &bytes.Buffer{}, gcEnv(gcAPI), func(context.Context, string, string) (int, error) {
		return 0, errDaemon
	})
	if !errors.Is(err, errDaemon) || !strings.Contains(err.Error(), "127.0.0.1:10102") {
		t.Fatalf("err = %v, want the cause and the daemon's address", err)
	}
}

// A cancellation that the process did not ask for (the daemon's side hung up
// with context.Canceled) is not a stop.
func TestRunIPFSGC_canceledErrorWithoutAStopIsAFailure(t *testing.T) {
	err := runIPFSGC(context.Background(), &bytes.Buffer{}, gcEnv(gcAPI), func(context.Context, string, string) (int, error) {
		return 0, context.Canceled
	})
	if err == nil {
		t.Fatal("an error that is not a requested stop was swallowed")
	}
}

func TestRunIPFSGC_badEnvironmentCollectsNothing(t *testing.T) {
	for name, env := range map[string]func(string) string{
		"no address":       func(k string) string { return map[string]string{ipfsGCAuthEnv: "bearer:tok"}[k] },
		"no credential":    func(k string) string { return map[string]string{ipfsGCAPIEnv: gcAPI}[k] },
		"basic credential": func(k string) string { return map[string]string{ipfsGCAPIEnv: gcAPI, ipfsGCAuthEnv: "basic:u:p"}[k] },
	} {
		called := false
		err := runIPFSGC(context.Background(), &bytes.Buffer{}, env, func(context.Context, string, string) (int, error) {
			called = true
			return 0, nil
		})
		if err == nil || called {
			t.Errorf("%s: err = %v, collected = %v", name, err, called)
		}
	}
}

// The command under a real SIGTERM, as systemd sends it on a stop, against a
// daemon that is still collecting: it exits 0 at once.
func TestIPFSGCCmd_sigtermEndsACollectionCleanly(t *testing.T) {
	collecting := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Key":{"/":"bafy1"}}` + "\n"))
		w.(http.Flusher).Flush()
		close(collecting)
		<-r.Context().Done()
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	host, port, _ := strings.Cut(addr, ":")
	t.Setenv(ipfsGCAPIEnv, "/ip4/"+host+"/tcp/"+port)
	t.Setenv(ipfsGCAuthEnv, "bearer:tok")

	var out bytes.Buffer
	ipfsGCCmd.SetOut(&out)
	ipfsGCCmd.SetContext(context.Background())
	done := make(chan error, 1)
	go func() { done <- ipfsGCCmd.RunE(ipfsGCCmd, nil) }()
	<-collecting
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SIGTERM during a collection: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the command kept collecting after SIGTERM")
	}
	if !strings.Contains(out.String(), "stopped before the collection finished") {
		t.Errorf("output = %q", out.String())
	}
}

func TestIPFSGCCmd_isHiddenAndNodeLocal(t *testing.T) {
	if !ipfsGCCmd.Hidden || !cmdmeta.IsNodeLocal(ipfsGCCmd) {
		t.Fatal("a unit's ExecStart runs with no home and is not an operator command")
	}
}
