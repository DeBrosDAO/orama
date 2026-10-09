package health

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// closedPort is a local port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// The health path is the deployer's own and may carry a token in its query
// string; the client's error quotes the whole URL. The log keeps the path.
func TestCheckDeployment_failureLogDropsTheQueryString(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	hc := &HealthChecker{logger: zap.New(core)}

	healthy := hc.checkDeployment(context.Background(), deploymentRow{
		Name: "shop", Namespace: "acme", Port: closedPort(t), HealthCheckPath: "/healthz?token=SECRET-QUERY-VALUE",
	})
	if healthy {
		t.Fatal("a closed port was reported healthy")
	}
	entries := logs.FilterMessage("Health check failed").All()
	if len(entries) != 1 {
		t.Fatalf("want one failure entry, got %d", len(entries))
	}
	got := fmt.Sprintf("%v", entries[0].ContextMap())
	if strings.Contains(got, "SECRET-QUERY-VALUE") || strings.Contains(got, "token=") {
		t.Errorf("the health-check failure log quotes the query: %s", got)
	}
	if !strings.Contains(got, "/healthz") || !strings.Contains(got, "refused") {
		t.Errorf("the log lost the path or the cause: %s", got)
	}
}

func TestCheckDeployment_badPathLogDropsTheQueryString(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	hc := &HealthChecker{logger: zap.New(core)}

	if hc.checkDeployment(context.Background(), deploymentRow{
		Name: "shop", Port: 8080, HealthCheckPath: "/h ealthz?token=SECRET-QUERY-VALUE\x7f",
	}) {
		t.Fatal("an unparseable path was reported healthy")
	}
	for _, e := range logs.All() {
		if got := fmt.Sprintf("%v", e.ContextMap()); strings.Contains(got, "SECRET-QUERY-VALUE") {
			t.Errorf("the log quotes the query: %s", got)
		}
	}
	if logs.Len() == 0 {
		t.Fatal("nothing was logged, so the test checks nothing")
	}
}
