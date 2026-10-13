package harness

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const childTXTValue = "verify-token-123"

// TestChildBroker (child only) writes a TXT record through the runner's
// broker; the parent checks it was set and deleted again at cleanup.
func TestChildBroker(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		return
	}
	name := "_orama-verify.app." + CustomDomain(t, "cd")
	if name != "_orama-verify.app.e2e-ab12-cd.dbrsteting.bid" {
		t.Fatalf("custom domain name %q", name)
	}
	DNSTXT(t, name, childTXTValue)
	dir := WorkTemp(t)
	if !strings.HasPrefix(dir, current.workDir+string(os.PathSeparator)) {
		t.Fatalf("WorkTemp %s is not under the work dir %s", dir, current.workDir)
	}
	if CLI(t).WorkDir != current.workDir {
		t.Fatal("the CLI runner does not know the work dir")
	}
}

// memZone is the broker's zone in memory.
type memZone struct {
	mu  sync.Mutex
	ops []string
}

func (z *memZone) RunIDOf(name string) (string, bool) {
	rest, ok := strings.CutSuffix(name, "."+cloudflare.AllowedZone)
	if !ok {
		return "", false
	}
	labels := strings.Split(rest, ".")
	owned, ok := strings.CutPrefix(labels[len(labels)-1], "e2e-")
	id, _, _ := strings.Cut(owned, "-")
	return id, ok
}

func (z *memZone) SetTXT(_ context.Context, name, value string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.ops = append(z.ops, "set "+name+" "+value)
	return nil
}

func (z *memZone) DeleteTXT(_ context.Context, name, value string) ([]cloudflare.Record, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.ops = append(z.ops, "delete "+name+" "+value)
	return nil, nil
}

func (z *memZone) RunRecords(context.Context) ([]cloudflare.Record, error) { return nil, nil }

// TestDNSTXT_throughTheBrokerWithCleanup runs a feature process with a
// state and a broker socket and no credential.
func TestDNSTXT_throughTheBrokerWithCleanup(t *testing.T) {
	dir, path := childState(t, nil)
	zone := &memZone{}
	srv := &broker.Server{State: &fleet.State{RunID: "ab12"}, DNS: zone, Cloud: noCloud{}}
	sockDir, err := os.MkdirTemp("", "hbrk")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	l, err := broker.Listen(context.Background(), sockDir, srv, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	out, code := runChildTest(t, "^TestChildBroker$", config.EnvState+"="+path, config.EnvStrict+"=1", broker.EnvSock+"="+l.Path)
	if code != 0 {
		t.Fatalf("code %d out:\n%s", code, out)
	}
	want := "set _orama-verify.app.e2e-ab12-cd.dbrsteting.bid " + childTXTValue + "," +
		"delete _orama-verify.app.e2e-ab12-cd.dbrsteting.bid " + childTXTValue
	if got := strings.Join(zone.ops, ","); got != want {
		t.Fatalf("zone ops %q, want %q", got, want)
	}
	if left, _ := os.ReadDir(dir); len(left) == 0 {
		t.Fatal("the work dir vanished")
	}
}

// noCloud refuses every server operation.
type noCloud struct{ broker.Cloud }
