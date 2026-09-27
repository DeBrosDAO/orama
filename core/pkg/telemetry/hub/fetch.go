package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// InternalReportPath is where a cluster gateway serves its own node's report
// to its peers. It answers only a coordination-MAC-signed request that came
// over the WireGuard mesh.
const InternalReportPath = "/v1/internal/telemetry"

// ReportAgeHeader carries how old the served report is, in milliseconds, as
// measured by the peer serving it. Age is computed on the peer's own clock, so
// a peer whose clock is off still reads as fresh or stale correctly; the
// clock-skew alert reports the skew itself.
const ReportAgeHeader = "X-Orama-Report-Age-Ms"

// ClockHeader carries the serving peer's clock, in Unix milliseconds, when it
// answered. With the request's send and receive times it gives the peer's
// clock offset, which the clock-skew alert compares.
const ClockHeader = "X-Orama-Clock-Ms"

// maxReportAge bounds the age a peer may claim for its report. Anything
// larger is not a stale report but a broken peer, and would overflow a
// time.Duration.
const maxReportAge = 24 * time.Hour

// maxReportBytes bounds a peer's report. A full report is tens of kilobytes;
// the bound stops a misbehaving peer from exhausting this gateway's memory.
const maxReportBytes = 4 << 20

// ErrPeerWithoutTelemetry is a peer that answered but does not serve
// telemetry: it runs an older release, as mid-way through a rolling upgrade.
// An older gateway knows no such route and refuses it as an unauthenticated
// request (401); this release answers a request it will not serve with 404, so
// the two cannot be confused.
var ErrPeerWithoutTelemetry = errors.New("runs a release that serves no telemetry")

// PeerReport is a peer's report, how old it was when served, and the peer's
// clock offset from this node's when the peer sent one.
type PeerReport struct {
	Report        *report.NodeReport
	Age           time.Duration
	ClockOffset   time.Duration
	ClockMeasured bool
}

// PeerFetcher gets one peer's latest report.
type PeerFetcher interface {
	Fetch(ctx context.Context, p Peer) (PeerReport, error)
}

// HTTPFetcher asks a peer's cluster gateway over the mesh.
type HTTPFetcher struct {
	Client *http.Client
	// BaseURL maps a peer's WireGuard address to its gateway's base URL.
	BaseURL func(wgIP string) string
	// Sign authenticates the request as coming from inside the cluster.
	Sign func(r *http.Request) error
}

// Fetch GETs the peer's report. The request is sent only to an address on the
// WireGuard mesh: it carries a MAC, and a registry row pointing elsewhere must
// not route a signed request off the overlay.
func (f HTTPFetcher) Fetch(ctx context.Context, p Peer) (PeerReport, error) {
	addr, err := netip.ParseAddr(p.WGIP)
	if err != nil || !constants.WireGuardOverlay().Contains(addr) {
		return PeerReport{}, fmt.Errorf("node %s has no WireGuard address in dns_nodes (internal_ip %q is not in %s)",
			p.ID, p.WGIP, constants.WireGuardSubnet)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.BaseURL(p.WGIP)+InternalReportPath, nil)
	if err != nil {
		return PeerReport{}, fmt.Errorf("build telemetry request for %s: %w", p.WGIP, err)
	}
	if err := f.Sign(req); err != nil {
		return PeerReport{}, fmt.Errorf("sign telemetry request: %w", err)
	}
	sent := time.Now()
	resp, err := f.Client.Do(req)
	if err != nil {
		return PeerReport{}, fmt.Errorf("gateway at %s did not answer (is its WireGuard tunnel up and orama-namespace-gateway@index running?): %w", p.WGIP, err)
	}
	received := time.Now()
	defer resp.Body.Close()
	pr, err := readPeerReport(resp, p.WGIP)
	if err != nil {
		return pr, err
	}
	pr.ClockOffset, pr.ClockMeasured = clockOffset(resp.Header.Get(ClockHeader), sent, received)
	return pr, nil
}

// clockOffset is the peer's clock minus this node's, from the clock the peer
// sent in its answer. The peer read its clock somewhere between sending and
// receiving; the midpoint bounds the error by half the round trip. The clock
// is a side measurement, not a condition of the report: a peer that sends
// none (0.122.109 did not) or an unreadable one leaves it unmeasured, and the
// report still counts.
func clockOffset(header string, sent, received time.Time) (time.Duration, bool) {
	peerClock, err := strconv.ParseInt(header, 10, 64)
	if err != nil {
		return 0, false
	}
	mid := sent.Add(received.Sub(sent) / 2)
	return time.UnixMilli(peerClock).Sub(mid), true
}

func readPeerReport(resp *http.Response, wgIP string) (PeerReport, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReportBytes+1))
	if err != nil {
		return PeerReport{}, fmt.Errorf("read telemetry from %s: %w", wgIP, err)
	}
	if len(body) > maxReportBytes {
		return PeerReport{}, fmt.Errorf("telemetry from %s is over %d bytes", wgIP, maxReportBytes)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return PeerReport{}, ErrPeerWithoutTelemetry
	case http.StatusNotFound:
		return PeerReport{}, fmt.Errorf("gateway at %s refused the signed request (does its cluster secret match this node's?)", wgIP)
	default:
		return PeerReport{}, fmt.Errorf("gateway at %s answered HTTP %d: %s", wgIP, resp.StatusCode, firstLine(body))
	}
	var r report.NodeReport
	if err := json.Unmarshal(body, &r); err != nil {
		return PeerReport{}, fmt.Errorf("parse telemetry from %s: %w", wgIP, err)
	}
	ms, err := strconv.ParseInt(resp.Header.Get(ReportAgeHeader), 10, 64)
	if err != nil || ms < 0 || ms > maxReportAge.Milliseconds() {
		return PeerReport{}, fmt.Errorf("gateway at %s sent no valid %s header", wgIP, ReportAgeHeader)
	}
	return PeerReport{Report: &r, Age: time.Duration(ms) * time.Millisecond}, nil
}

// firstLine is the start of an error body, enough to say what went wrong.
func firstLine(b []byte) string {
	const maxLen = 200
	for i, c := range b {
		if c == '\n' || i == maxLen {
			return string(b[:i])
		}
	}
	return string(b)
}
