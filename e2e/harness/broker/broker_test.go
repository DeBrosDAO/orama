package broker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const verifyName = "_orama-verify.app.e2e-" + testRun + "-cd.dbrsteting.bid"

func TestSetTXT_runNameCreatedAndDeleted(t *testing.T) {
	zone, c, _ := serve(t, &fakeCloud{})
	ctx := context.Background()
	if err := c.SetTXT(ctx, verifyName, "token-abc"); err != nil {
		t.Fatal(err)
	}
	recs, err := c.Records(ctx, "")
	if err != nil || len(recs) != 1 || recs[0].Name != verifyName || recs[0].Type != "TXT" {
		t.Fatalf("records %+v err %v", recs, err)
	}
	if err := c.DeleteTXT(ctx, verifyName, "token-abc"); err != nil {
		t.Fatal(err)
	}
	if len(zone.records) != 0 {
		t.Fatalf("left %+v", zone.records)
	}
}

func TestSetTXT_foreignNamesRefusedWithoutAPICall(t *testing.T) {
	zone, c, _ := serve(t, &fakeCloud{})
	for _, name := range []string{"dbrsteting.bid", "www.dbrsteting.bid", "x.e2e-" + otherRun + ".dbrsteting.bid",
		"e2e-" + otherRun + "-cd.dbrsteting.bid", "x.e2e-" + testRun + ".example.com", ""} {
		if err := c.SetTXT(context.Background(), name, "v"); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("SetTXT(%q): %v", name, err)
		}
		if err := c.DeleteTXT(context.Background(), name, ""); err == nil {
			t.Errorf("DeleteTXT(%q) allowed", name)
		}
	}
	if zone.calls != 0 {
		t.Fatalf("%d refused requests reached Cloudflare", zone.calls)
	}
}

func TestRecords_onlyThisRun(t *testing.T) {
	zone, c, _ := serve(t, &fakeCloud{})
	zone.add("NS", "e2e-"+testRun+".dbrsteting.bid", "ns1.e2e-"+testRun+".dbrsteting.bid")
	zone.add("TXT", "x.e2e-"+otherRun+".dbrsteting.bid", "other")
	zone.add("A", "www.dbrsteting.bid", "203.0.113.1")
	recs, err := c.Records(context.Background(), "")
	if err != nil || len(recs) != 1 || recs[0].Type != "NS" {
		t.Fatalf("records %+v err %v", recs, err)
	}
	if _, err := c.Records(context.Background(), "e2e-"+otherRun+".dbrsteting.bid"); err == nil {
		t.Fatal("listing another run was allowed")
	}
}

func TestExtra_addRemoveAndScope(t *testing.T) {
	cloud := &fakeCloud{}
	_, c, _ := serve(t, cloud)
	ctx := context.Background()
	n, err := c.AddExtra(ctx, "extra-9", "hel1")
	if err != nil || n.Name != "extra-9" || n.Location != "hel1" {
		t.Fatalf("node %+v err %v", n, err)
	}
	for _, name := range []string{"extra-9", "node-1", "extra-state"} {
		if _, err := c.AddExtra(ctx, name, "hel1"); err == nil {
			t.Errorf("a second %s was created", name)
		}
	}
	for _, name := range []string{"node-1", "probe-1", "never-made"} {
		if err := c.RemoveExtra(ctx, name); err == nil {
			t.Errorf("RemoveExtra(%s) allowed", name)
		}
	}
	for _, name := range []string{"extra-9", "extra-state"} {
		if err := c.RemoveExtra(ctx, name); err != nil {
			t.Fatalf("RemoveExtra(%s): %v", name, err)
		}
	}
	if strings.Join(cloud.removes, ",") != "extra-9,extra-state" {
		t.Fatalf("removes %v", cloud.removes)
	}
}

func TestExtra_concurrentSameNameOneWins(t *testing.T) {
	cloud := &fakeCloud{}
	_, c, _ := serve(t, cloud)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.AddExtra(context.Background(), "extra-race", "nbg1"); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 || len(cloud.adds) != 1 {
		t.Fatalf("%d adds succeeded, %d reached the cloud", ok, len(cloud.adds))
	}
}

func TestCluster_addRemoveOnlyOwn(t *testing.T) {
	_, c, _ := serve(t, &fakeCloud{})
	ctx := context.Background()
	if err := c.RemoveCluster(ctx, "evalx"); err == nil {
		t.Fatal("removing a cluster the broker never installed was allowed")
	}
	cl, err := c.AddCluster(ctx, "evalx")
	if err != nil || cl.Env != "e2e-"+testRun+"-evalx" {
		t.Fatalf("cluster %+v err %v", cl, err)
	}
	if _, err := c.AddCluster(ctx, "evalx"); err == nil {
		t.Fatal("a second evalx was installed")
	}
	if err := c.RemoveCluster(ctx, "evalx"); err != nil {
		t.Fatal(err)
	}
}

func TestCall_clientCancelCancelsTheOperation(t *testing.T) {
	cloud := &fakeCloud{block: true, started: make(chan struct{}), ended: make(chan error, 1)}
	_, c, _ := serve(t, cloud)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.AddExtra(ctx, "extra-slow", "nbg1"); done <- err }()
	<-cloud.started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a cancelled call succeeded")
	}
	select {
	case err := <-cloud.ended:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the operation ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the operation kept running after the client went away")
	}
}

func TestListen_modesAndRefusals(t *testing.T) {
	_, _, l := serve(t, &fakeCloud{})
	sock, err := os.Lstat(l.Path)
	if err != nil || sock.Mode().Perm() != socketMode {
		t.Fatalf("socket %v err %v", sock, err)
	}
	dir, _ := os.Lstat(filepath.Dir(l.Path))
	if dir.Mode().Perm() != dirMode {
		t.Fatalf("dir mode %v", dir.Mode())
	}
	inFeature := func(string) (string, bool) { return "/x/broker.sock", true }
	if _, err := Listen(context.Background(), t.TempDir(), &Server{}, inFeature); err == nil {
		t.Fatal("a feature process was allowed to serve a broker")
	}
	if _, err := Listen(context.Background(), "/"+strings.Repeat("d", 120), &Server{}, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("an incomplete server or overlong path was accepted")
	}
}

func TestFromEnv_unsetRelativeAndSet(t *testing.T) {
	env := map[string]string{}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if c, err := FromEnv(lookup); c != nil || err != nil {
		t.Fatalf("unset: %v %v", c, err)
	}
	env[EnvSock] = "rel/broker.sock"
	if _, err := FromEnv(lookup); err == nil {
		t.Fatal("a relative socket path was accepted")
	}
	env[EnvSock] = "/run/x/broker.sock"
	if c, err := FromEnv(lookup); c == nil || err != nil {
		t.Fatalf("set: %v %v", c, err)
	}
}

func TestHandle_unknownOpAndRedaction(t *testing.T) {
	s := &Server{Redact: func(v string) string { return strings.ReplaceAll(v, "secret", "[REDACTED]") }}
	if r := s.Handle(context.Background(), Request{Op: "dns.zone.delete"}); !strings.Contains(r.Error, "unknown operation") {
		t.Fatalf("response %+v", r)
	}
	s.State, s.DNS = nil, nil
	if r := s.Handle(context.Background(), Request{Op: "secret.op"}); strings.Contains(r.Error, "secret") {
		t.Fatalf("not redacted: %q", r.Error)
	}
}
