//go:build e2e_fleet

package monitoring

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Telemetry API (website/src/docs/operator/monitoring.mdx "The telemetry API").
const (
	telemetryPath = "/v1/operator/telemetry"
	streamPath    = "/v1/operator/telemetry/stream"
	// snapshotCache is how long one assembly is reused.
	snapshotCache = 5 * time.Second
	// streamWait bounds reading two events at the fastest interval.
	streamWait = 30 * time.Second
	// maxSnapshotAge is the cache, one bounded assembly (15s) and slack.
	maxSnapshotAge = snapshotCache + 15*time.Second + 5*time.Second
	// pairRetryEvery and pairBudget pace taking pairs of reads until one
	// lands inside the cache window.
	pairRetryEvery = time.Second
	pairBudget     = 30 * time.Second
)

type snapshot struct {
	CollectedAt time.Time `json:"collected_at"`
	Nodes       []struct {
		Node struct {
			Host string `json:"host"`
		} `json:"node"`
		Report json.RawMessage `json:"report"`
	} `json:"nodes"`
}

// TestTelemetry_refusedToNonOperators: the snapshot and the stream are
// refused to no credential (401) and to a namespace owner who is not on the
// operator list (403 NOT_AN_OPERATOR) (docs/whitepaper/technical-reference/vol1/12-gateway-architecture.md "What the open
// health and status endpoints show": operator grant and the operator list).
// The refusal is held to {error, code} here; its documented hint is the
// subtest's, so the product bug that leaves it out fails one check.
func TestTelemetry_refusedToNonOperators(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	stranger := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for _, path := range []string{telemetryPath, streamPath} {
		c.MustSend(t, gw.Req{Path: path}).Expect(t, http.StatusUnauthorized)
		expectNotOperator(t, c.MustSend(t, gw.Req{Path: path, Bearer: stranger.Owner.Token()}))
	}
	t.Run("NOT_AN_OPERATOR carries a hint", func(t *testing.T) {
		resp := c.MustSend(t, gw.Req{Path: telemetryPath, Bearer: stranger.Owner.Token()})
		hint, _ := expectNotOperator(t, resp)["hint"].(string)
		if strings.TrimSpace(hint) == "" {
			t.Errorf("PRODUCT BUG: 403 %s carries no hint (core/pkg/gateway/handlers/operator/authorize.go requireOperator "+
				"writes only error and code), although docs/whitepaper/technical-reference/vol1/14-authorization.md#refusals-and-the-error-code-table promises {error, code, hint}: %s",
				tenancy.CodeNotOperator, resp.Body)
		}
	})
}

// expectNotOperator fails unless resp is 403 NOT_AN_OPERATOR with an error
// text, and returns the body.
func expectNotOperator(t *testing.T, resp *gw.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil || resp.Status != http.StatusForbidden || body["code"] != tenancy.CodeNotOperator {
		t.Fatalf("want HTTP 403 %s, got %d: %.300s", tenancy.CodeNotOperator, resp.Status, resp.Body)
	}
	if s, _ := body["error"].(string); strings.TrimSpace(s) == "" {
		t.Errorf("HTTP 403 %s carries no error text: %.300s", tenancy.CodeNotOperator, resp.Body)
	}
	return body
}

// TestTelemetry_asOperator runs the telemetry tests that need an operator
// against ONE shared fixture (operator): the cluster's operator list is
// changed once, by this test, and restored when its parallel subtests end.
func TestTelemetry_asOperator(t *testing.T) {
	t.Parallel()
	op := operator(t)
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *ns.Namespace)
	}{
		{"served to an operator", servedToAnOperator},
		{"snapshot reused for five seconds", snapshotReusedForFiveSeconds},
		{"stream sends snapshots and bounds its interval", streamSendsSnapshotsAndBoundsInterval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, op)
		})
	}
}

// servedToAnOperator: an operator reads a snapshot of every node.
func servedToAnOperator(t *testing.T, op *ns.Namespace) {
	var snap snapshot
	if err := harness.GW(t).MustSend(t, gw.Req{Path: telemetryPath, Bearer: op.Owner.Token()}).Expect(t, http.StatusOK).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Nodes) != len(harness.Fleet(t).State.Nodes) {
		t.Errorf("the snapshot has %d nodes, want %d", len(snap.Nodes), len(harness.Fleet(t).State.Nodes))
	}
}

// snapshotReusedForFiveSeconds: two reads of one gateway in quick
// succession are the same assembly (the same collected_at), and no read is
// older than the cache plus one assembly (website/src/docs/operator/monitoring.mdx "Aggregation":
// reused for 5s from when it finished, bounded at 15s). A pair can straddle
// the moment one assembly expires, or be too far apart, so pairs are taken
// until one lands inside the window; none ever sharing an assembly fails.
func snapshotReusedForFiveSeconds(t *testing.T, op *ns.Namespace) {
	c := harness.GW(t).PinTo(harness.Fleet(t).State.Nodes[0].PublicIP)
	read := func() (snapshot, time.Time, error) {
		var s snapshot
		resp := c.MustSend(t, gw.Req{Path: telemetryPath, Bearer: op.Owner.Token()})
		if resp.Status != http.StatusOK {
			return s, time.Time{}, eventually.Stop(fmt.Errorf("GET %s: HTTP %d %.200s", telemetryPath, resp.Status, resp.Body))
		}
		return s, time.Now(), resp.Decode(&s)
	}
	eventually.Require(t, pairRetryEvery, pairBudget, "two quick reads to share one assembly", func() (bool, error) {
		first, at, err := read()
		if err != nil {
			return false, err
		}
		second, at2, err := read()
		if err != nil {
			return false, err
		}
		if gap := at2.Sub(at); gap >= snapshotCache/2 {
			return false, fmt.Errorf("the reads were %s apart, too far to share an assembly", gap)
		}
		if !first.CollectedAt.Equal(second.CollectedAt) {
			return false, fmt.Errorf("two reads %s apart assembled twice (%s, %s)", at2.Sub(at), first.CollectedAt, second.CollectedAt)
		}
		if age := at2.Sub(second.CollectedAt); age > maxSnapshotAge {
			return false, eventually.Stop(fmt.Errorf("a snapshot %s old was served", age))
		}
		return true, nil
	})
}

// streamSendsSnapshotsAndBoundsInterval: the stream answers
// text/event-stream with `event: snapshot` blocks at the asked interval,
// and refuses an interval outside 2-60 whole seconds with 400
// (docs/whitepaper/technical-reference/appendices/i-api-surface.md "/v1/operator/telemetry/stream").
func streamSendsSnapshotsAndBoundsInterval(t *testing.T, op *ns.Namespace) {
	c := harness.GW(t)
	for _, bad := range []string{"1", "61", "0", "-5", "2.5", "abc"} {
		resp := c.MustSend(t, gw.Req{Path: streamPath + "?interval=" + bad, Bearer: op.Owner.Token()})
		if resp.Status != http.StatusBadRequest {
			t.Errorf("interval=%s: %d, want 400", bad, resp.Status)
		}
	}
	events := readStream(t, c, op.Owner.Token(), 2)
	for _, e := range events {
		var s snapshot
		if err := json.Unmarshal([]byte(e), &s); err != nil || s.CollectedAt.IsZero() {
			t.Errorf("a snapshot event is not one snapshot on one line: %v %.200s", err, e)
		}
	}
}

// readStream opens the stream at the minimum interval and returns the data
// lines of the first n snapshot events.
func readStream(t *testing.T, c *gw.Client, token string, n int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), streamWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+streamPath+"?interval=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		t.Fatalf("open the telemetry stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var out []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	event := ""
	for len(out) < n && sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && event == "snapshot":
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	if len(out) < n {
		t.Fatalf("read %d snapshot events in %s, want %d: %v", len(out), streamWait, n, sc.Err())
	}
	return out
}
