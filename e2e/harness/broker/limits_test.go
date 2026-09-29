package broker

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestAddExtra_serverCapRefusesOverTheCap(t *testing.T) {
	_, c, _ := serve(t, &fakeCloud{})
	ctx := context.Background()
	for i := 0; i < DefaultMaxServers; i++ {
		if _, err := c.AddExtra(ctx, "extra-"+string(rune('a'+i)), "hel1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.AddExtra(ctx, "extra-over", "hel1"); err == nil || !strings.Contains(err.Error(), EnvMaxServers) {
		t.Fatalf("a server over the cap was created: %v", err)
	}
	if _, err := c.AddCluster(ctx, "evalx"); err == nil {
		t.Fatal("a cluster over the cap was installed")
	}
}

func TestSetTXT_capOnLiveNames(t *testing.T) {
	_, c, _ := serve(t, &fakeCloud{})
	ctx := context.Background()
	name := func(i int) string {
		return "_v" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ".e2e-" + testRun + ".dbrsteting.bid"
	}
	for i := 0; i < maxTXTRecords; i++ {
		if err := c.SetTXT(ctx, name(i), "v"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SetTXT(ctx, name(maxTXTRecords), "v"); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("a TXT name over the cap was set: %v", err)
	}
	if err := c.SetTXT(ctx, name(0), "v2"); err != nil {
		t.Fatalf("a second value of a held name was refused: %v", err)
	}
	if err := c.DeleteTXT(ctx, name(0), ""); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTXT(ctx, name(maxTXTRecords), "v"); err != nil {
		t.Fatalf("a freed slot was not reusable: %v", err)
	}
}

// TestServeConn_silentClientTimesOutAndFreesItsSlot: with one slot, a
// client that connects and never sends holds it only until the read
// deadline; the next request is served after it.
func TestServeConn_silentClientTimesOutAndFreesItsSlot(t *testing.T) {
	oldTimeout, oldConns := requestReadTimeout, maxConns
	requestReadTimeout, maxConns = 200*time.Millisecond, 1
	t.Cleanup(func() { requestReadTimeout, maxConns = oldTimeout, oldConns })
	_, c, l := serve(t, &fakeCloud{})
	silent, err := net.Dial("unix", l.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Records(ctx, ""); err != nil {
		t.Fatalf("the request behind a silent client was not served: %v", err)
	}
	buf := make([]byte, 512)
	n, _ := silent.Read(buf)
	if !strings.Contains(string(buf[:n]), "bad request") {
		t.Fatalf("the silent client got %q", buf[:n])
	}
}
