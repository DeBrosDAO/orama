package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func fixedReport() *report.NodeReport {
	return &report.NodeReport{
		Timestamp: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		Hostname:  "athena",
		Version:   "0.200.0",
		Chain:     &report.ChainReport{ServiceActive: true, Responsive: true, LatestHeight: 42},
	}
}

// useTempNodeReportLock points the node-report lock at a fresh file and
// optionally shortens the collection deadline.
func useTempNodeReportLock(t *testing.T, timeout time.Duration) {
	t.Helper()
	oldPath, oldTimeout := nodeReportLockPath, nodeReportTimeout
	t.Cleanup(func() { nodeReportLockPath, nodeReportTimeout = oldPath, oldTimeout })
	nodeReportLockPath = filepath.Join(t.TempDir(), "node-report.lock")
	if timeout > 0 {
		nodeReportTimeout = timeout
	}
}

// blockingCollector signals started when it runs and returns once release
// is closed.
func blockingCollector(started chan<- struct{}, release <-chan struct{}) func() *report.NodeReport {
	return func() *report.NodeReport {
		started <- struct{}{}
		<-release
		return fixedReport()
	}
}

func TestNodeReport_producesTheReportAsJSON(t *testing.T) {
	useTempNodeReportLock(t, 0)
	resp := nodeReport(fixedReport)
	if resp.ExitCode != 0 {
		t.Fatalf("exit %d: %s", resp.ExitCode, resp.Output)
	}
	var got report.NodeReport
	if err := json.Unmarshal([]byte(resp.Output), &got); err != nil {
		t.Fatalf("output is not a node report: %v\n%s", err, resp.Output)
	}
	if got.Hostname != "athena" || got.Chain == nil || got.Chain.LatestHeight != 42 {
		t.Errorf("round trip lost data: %+v", got)
	}
	if strings.Contains(resp.Output, "\n") {
		t.Error("the report must be compact JSON")
	}
}

func TestNodeReport_encodeFailureIsNonZero(t *testing.T) {
	useTempNodeReportLock(t, 0)
	resp := nodeReport(func() *report.NodeReport {
		r := fixedReport()
		r.Chain.AvgBlockTimeSec = math.NaN() // encoding/json cannot encode NaN
		return r
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Output, "encode the node report") {
		t.Fatalf("got %+v, want a failure naming the encoding", resp)
	}
}

// A ~200KB report — a busy node with many services and namespaces — must
// reach the client whole: the client reads at most MaxRequestBytes.
func TestNodeReport_largeReportFitsTheClientLimit(t *testing.T) {
	useTempNodeReportLock(t, 0)
	big := fixedReport()
	big.Services = &report.ServicesReport{}
	for i := 0; i < 1500; i++ {
		big.Services.Services = append(big.Services.Services, report.ServiceInfo{
			Name:        fmt.Sprintf("orama-deploy-node@tenant%04d-web.service", i),
			ActiveState: "active",
		})
	}
	resp := nodeReport(func() *report.NodeReport { return big })
	if resp.ExitCode != 0 {
		t.Fatalf("exit %d: %.200s", resp.ExitCode, resp.Output)
	}
	if n := len(resp.Output); n < 150_000 {
		t.Fatalf("fixture is only %d bytes; the test needs a ~200KB report", n)
	}
	var wire bytes.Buffer
	if err := json.NewEncoder(&wire).Encode(resp); err != nil {
		t.Fatal(err)
	}
	var got privhelper.Response
	if err := json.NewDecoder(io.LimitReader(&wire, privhelper.MaxRequestBytes)).Decode(&got); err != nil {
		t.Fatalf("the client could not read the response: %v", err)
	}
	if got.Output != resp.Output {
		t.Error("the report changed on the wire")
	}
}

func TestNodeReport_oversizedReportIsRefusedNotTruncated(t *testing.T) {
	useTempNodeReportLock(t, 0)
	resp := nodeReport(func() *report.NodeReport {
		r := fixedReport()
		r.Errors = []string{strings.Repeat("x", privhelper.MaxRequestBytes)}
		return r
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Output, "response limit") {
		t.Fatalf("exit %d, output %.200s; want a failure naming the limit", resp.ExitCode, resp.Output)
	}
}

func TestHandle_nodeReportRefusesInput(t *testing.T) {
	_, err := handle(privhelper.Caller{UID: 0}, privhelper.Request{
		Argv:  []string{privhelper.ToolNodeReport},
		Input: "{}",
	})
	if err == nil || !strings.Contains(err.Error(), "takes no input") {
		t.Fatalf("err = %v", err)
	}
}

func TestHandle_tenantGatewayMayNotCollectTheReport(t *testing.T) {
	resp, err := handle(privhelper.Caller{UID: 1000, Unit: "orama-namespace-gateway@alice.service"},
		privhelper.Request{Argv: []string{privhelper.ToolNodeReport}})
	if err == nil || resp.ExitCode != privhelper.ExitRefused {
		t.Fatalf("a tenant gateway was allowed the node report: %+v, %v", resp, err)
	}
}

func TestNodeReport_concurrentCollectionIsRefused(t *testing.T) {
	useTempNodeReportLock(t, 0)
	started, release := make(chan struct{}, 1), make(chan struct{})
	first := make(chan privhelper.Response, 1)
	go func() { first <- nodeReport(blockingCollector(started, release)) }()
	<-started

	second := nodeReport(fixedReport)
	if second.ExitCode == 0 || !strings.Contains(second.Output, "already running") {
		t.Fatalf("a second collection ran alongside the first: %+v", second)
	}

	close(release)
	if resp := <-first; resp.ExitCode != 0 {
		t.Fatalf("the first collection failed: %s", resp.Output)
	}
	if resp := nodeReport(fixedReport); resp.ExitCode != 0 {
		t.Errorf("the lock was not released after the first collection: %s", resp.Output)
	}
}

func TestNodeReport_hungCollectionTimesOut(t *testing.T) {
	useTempNodeReportLock(t, 50*time.Millisecond)
	started, release := make(chan struct{}, 1), make(chan struct{})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	resp := nodeReport(blockingCollector(started, release))
	if resp.ExitCode == 0 || !strings.Contains(resp.Output, "did not finish within 50ms") {
		t.Fatalf("got %+v; want a failure naming the deadline", resp)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("returned after %v, not at the deadline", elapsed)
	}
	// The hung collection still holds the lock: a new one must not start
	// beside it.
	if again := nodeReport(fixedReport); !strings.Contains(again.Output, "already running") {
		t.Errorf("a collection started beside a hung one: %+v", again)
	}
}

func TestNodeReport_unopenableLockIsAFailure(t *testing.T) {
	useTempNodeReportLock(t, 0)
	nodeReportLockPath = filepath.Join(t.TempDir(), "missing-dir", "node-report.lock")
	resp := nodeReport(func() *report.NodeReport {
		t.Error("collected without the lock")
		return fixedReport()
	})
	if resp.ExitCode == 0 || !strings.Contains(resp.Output, "open the node report lock") {
		t.Fatalf("got %+v", resp)
	}
}
