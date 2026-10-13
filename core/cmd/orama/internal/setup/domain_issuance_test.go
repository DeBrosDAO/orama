package setup

import (
	"context"
	"crypto/tls"
	"crypto/x509/pkix"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/tlsstatus"
)

const issuanceDomain = "cluster.example.org"

var issuanceNow = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// The live stagenet failure of 2026-10-11: Let's Encrypt refused with HTTP 429
// and said when it would accept the order again.
func rateLimited(retry time.Time) *tlsstatus.Report {
	return &tlsstatus.Report{Names: []tlsstatus.Name{
		{Name: "*." + issuanceDomain, ServeError: "tls: internal error"},
		{Name: issuanceDomain, ServeError: "tls: internal error", LastError: &tlsstatus.ObtainError{
			At: issuanceNow.Add(-time.Hour), CA: "https://acme-v02.api.letsencrypt.org/directory", Status: 429, Type: "rateLimited",
			Message:    "too many certificates (5) already issued for this exact set of identifiers in the last 168h0m0s",
			RetryAfter: retry,
		}},
	}}
}

// issuanceCluster is a delegated cluster whose node answers status and serves dial.
func issuanceCluster(status *tlsstatus.Report, statusErr error) (nsMachine, clusterDomain, *int) {
	m := clusterWith(delegation())
	m.fakeMachine.tlsStatus, m.fakeMachine.tlsStatusErr = status, statusErr
	dials := 0
	c := clusterDomain{
		check: func(context.Context, dnsdelegation.Delegation) ([]dnsdelegation.Finding, error) { return nil, nil },
		dialCert: func(context.Context, string) (*tls.ConnectionState, error) {
			dials++
			return nil, errors.New("remote error: tls: internal error")
		},
		clock: func() time.Time { return issuanceNow },
	}
	return m, c, &dials
}

func TestClusterDomain_readyNamesTheCAsRateLimit(t *testing.T) {
	m, c, _ := issuanceCluster(rateLimited(issuanceNow.Add(15*time.Hour)), nil)
	done, err := c.ready(context.Background(), m, issuanceDomain)
	if done || err == nil {
		t.Fatalf("done=%v err=%v", done, err)
	}
	for _, want := range []string{
		"the cluster's certificate: remote error: tls: internal error",
		"refused to issue a certificate for " + issuanceDomain + " because of its rate limit",
		"too many certificates (5) already issued",
		"accepts the order again after 2026-10-12 03:00:00 UTC (in 15h0m0s)",
		"--acme-ca letsencrypt-staging",
		"orama network add <name> <url> --ca-file",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the reason lacks %q:\n%v", want, err)
		}
	}
	if m.fakeMachine.w.count("tls status on") != 1 {
		t.Error("the node was asked for its certificate status once")
	}
}

// A rate limit that clears after the wait would end cannot be waited out.
func TestClusterDomain_waitEndsEarlyOnARateLimitBeyondTheDeadline(t *testing.T) {
	m, c, dials := issuanceCluster(rateLimited(issuanceNow.Add(15*time.Hour)), nil)
	start := time.Now()
	err := c.Wait(context.Background(), m, issuanceDomain, time.Millisecond, time.Hour)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "because of its rate limit") || strings.Contains(err.Error(), "gave up waiting") {
		t.Errorf("the wait should end with the CA's reason, not a timeout:\n%v", err)
	}
	if *dials != 1 || time.Since(start) > time.Second {
		t.Errorf("%d looks in %v: a refusal that cannot clear ends the wait at the first look", *dials, time.Since(start))
	}
	var refused *issuanceRefused
	if !errors.As(err, &refused) || !refused.Err.RateLimited() {
		t.Errorf("the error is not an issuanceRefused: %v", err)
	}
}

func TestClusterDomain_waitKeepsPollingWhenTheRateLimitClearsInTime(t *testing.T) {
	m, c, dials := issuanceCluster(rateLimited(issuanceNow.Add(time.Minute)), nil)
	c.dialCert = func(context.Context, string) (*tls.ConnectionState, error) {
		*dials++
		if *dials < 3 {
			return nil, errors.New("remote error: tls: internal error")
		}
		return leafState(t, pkix.Name{CommonName: issuanceDomain}, pkix.Name{CommonName: "CA"}, []string{issuanceDomain}, time.Now().Add(time.Hour)), nil
	}
	if err := c.Wait(context.Background(), m, issuanceDomain, time.Millisecond, time.Hour); err != nil || *dials != 3 {
		t.Fatalf("%d looks, %v", *dials, err)
	}
}

func TestClusterDomain_aRateLimitWithoutARetryTimeIsPolledToTheDeadline(t *testing.T) {
	m, c, dials := issuanceCluster(rateLimited(time.Time{}), nil)
	err := c.Wait(context.Background(), m, issuanceDomain, time.Millisecond, 30*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "gave up waiting") || !strings.Contains(err.Error(), "because of its rate limit") {
		t.Fatalf("got %v", err)
	}
	if *dials < 2 {
		t.Errorf("%d looks: with no retry time the wait cannot know the limit will not clear", *dials)
	}
}

func TestClusterDomain_otherACMEAnswersExplainButDoNotEndTheWait(t *testing.T) {
	status := &tlsstatus.Report{Names: []tlsstatus.Name{{Name: issuanceDomain, LastError: &tlsstatus.ObtainError{
		At: issuanceNow, Status: 400, Type: "dns", Message: "DNS problem: SERVFAIL looking up TXT",
	}}}}
	m, c, dials := issuanceCluster(status, nil)
	err := c.Wait(context.Background(), m, issuanceDomain, time.Millisecond, 30*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "gave up waiting") {
		t.Fatalf("a failure that may clear is waited out: %v", err)
	}
	for _, want := range []string{"refused to issue a certificate for " + issuanceDomain, "HTTP 400 dns: DNS problem: SERVFAIL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	if *dials < 2 {
		t.Errorf("%d looks", *dials)
	}
}

func TestClusterDomain_aNodeThatCannotAnswerLeavesTheDialReasonAndSaysSo(t *testing.T) {
	m, c, _ := issuanceCluster(nil, errors.New("orama node tls status on 203.0.113.10: unknown command"))
	_, err := c.ready(context.Background(), m, issuanceDomain)
	if err == nil {
		t.Fatal("want a reason")
	}
	for _, want := range []string{"the cluster's certificate: remote error: tls: internal error", "the node's certificate status could not be read", "unknown command"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the reason lacks %q:\n%v", want, err)
		}
	}
}

func TestClusterDomain_noFailedOrderLeavesTheReasonAsItWas(t *testing.T) {
	status := &tlsstatus.Report{Names: []tlsstatus.Name{
		{Name: issuanceDomain, ServeError: "tls: internal error"},
		// A failure of another name, and of a name that is now held, explain nothing.
		{Name: "push." + issuanceDomain, LastError: &tlsstatus.ObtainError{Type: "rateLimited", Message: "x"}},
		{Name: "*." + issuanceDomain, Held: true, LastError: &tlsstatus.ObtainError{Type: "rateLimited", Message: "old"}},
	}}
	m, c, _ := issuanceCluster(status, nil)
	_, err := c.ready(context.Background(), m, issuanceDomain)
	if err == nil || err.Error() != "the cluster's certificate: remote error: tls: internal error" {
		t.Errorf("got %v", err)
	}
}

func TestClusterDomain_aRateLimitedWildcardIsTheClustersToo(t *testing.T) {
	status := &tlsstatus.Report{Names: []tlsstatus.Name{
		{Name: issuanceDomain, LastError: &tlsstatus.ObtainError{Type: "dns", Status: 400, Message: "dns"}},
		{Name: "*." + issuanceDomain, LastError: &tlsstatus.ObtainError{Type: "rateLimited", Status: 429, Message: "slow down", RetryAfter: issuanceNow.Add(time.Hour)}},
	}}
	m, c, _ := issuanceCluster(status, nil)
	_, err := c.ready(context.Background(), m, issuanceDomain)
	var refused *issuanceRefused
	if !errors.As(err, &refused) || refused.Name != "*."+issuanceDomain {
		t.Errorf("the rate limit is the one reported, got %v", err)
	}
}

func TestClusterDomain_theCAsTextCannotDriveTheTerminal(t *testing.T) {
	status := &tlsstatus.Report{Names: []tlsstatus.Name{{Name: issuanceDomain, LastError: &tlsstatus.ObtainError{
		Type: "rateLimited", Status: 429, Message: "slow\x1b[2J down", RetryAfter: issuanceNow.Add(time.Hour),
	}}}}
	m, c, _ := issuanceCluster(status, nil)
	_, err := c.ready(context.Background(), m, issuanceDomain)
	if err == nil || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("got %q: the log is text a node wrote", err)
	}
}

// The node's report crosses SSH as JSON; the machine runs the command with
// sudo on the staged binary and refuses what is not a report.
func TestSSHMachine_certificateStatusReadsTheNodesReport(t *testing.T) {
	body := `{"unit":"orama-namespace-caddy@index","ca":"https://acme-v02.api.letsencrypt.org/directory","names":[` +
		`{"name":"example.org","held":false,"serve_error":"tls: internal error","last_error":{"at":"2026-10-11T04:26:01Z","status":429,"type":"rateLimited","message":"too many","retry_after":"2026-10-12T04:26:01Z"}}]}`
	sh := &recShell{answers: map[string]string{"node tls status": body}}
	m, _ := testMachine(sh)
	r, err := m.CertificateStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Names) != 1 || r.Names[0].LastError == nil || !r.Names[0].LastError.RateLimited() ||
		!r.Names[0].LastError.RetryAfter.Equal(time.Date(2026, 10, 12, 4, 26, 1, 0, time.UTC)) {
		t.Errorf("%+v", r)
	}
	if call := sh.first("node tls status"); !strings.HasSuffix(call, m.sudo()+orama+" node tls status --json") {
		t.Errorf("ran %q", call)
	}
}

func TestSSHMachine_certificateStatusFailures(t *testing.T) {
	sh := &recShell{fail: map[string]error{"node tls status": errors.New("exit status 2")}}
	m, _ := testMachine(sh)
	if _, err := m.CertificateStatus(context.Background()); err == nil || !strings.Contains(err.Error(), "exit status 2") {
		t.Errorf("a failing command: %v", err)
	}
	sh = &recShell{answers: map[string]string{"node tls status": "Error: unknown command \"tls\""}}
	m, _ = testMachine(sh)
	if _, err := m.CertificateStatus(context.Background()); err == nil || !strings.Contains(err.Error(), "did not print a report") {
		t.Errorf("output that is not a report: %v", err)
	}
}

// The wait at the end of the run names a refused order the same way.
func TestRun_aRefusedCertificateAtTheEndSaysEverythingElseIsInstalled(t *testing.T) {
	h := newHarness()
	refusal := &issuanceRefused{Name: "cluster.example.org", Now: issuanceNow, Err: *rateLimited(issuanceNow.Add(15 * time.Hour)).Names[1].LastError}
	h.deps.Domain = &failSecondWait{fakeDomain: fakeDomain{w: h.w}, err: refusal}
	opts := h.opts(ip1, ip2)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	_, err := run(t, h, opts)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"has no certificate", "because of its rate limit", "everything else is installed", "resumes at this step"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
}

// failSecondWait waits once successfully (before the first join) and then fails.
type failSecondWait struct {
	fakeDomain
	err   error
	waits int
}

func (f *failSecondWait) Wait(ctx context.Context, via Machine, domain string, poll, deadline time.Duration) error {
	f.waits++
	if f.waits == 1 {
		return f.fakeDomain.Wait(ctx, via, domain, poll, deadline)
	}
	return f.err
}

// The first join waits for the certificate, and a refused order is reported as
// that, not as a delegation that has not happened.
func TestRun_aRefusedCertificateStopsBeforeTheJoinAndSaysWhy(t *testing.T) {
	h := newHarness()
	refusal := &issuanceRefused{Domain: "cluster.example.org", Name: "cluster.example.org", Now: issuanceNow, Err: *rateLimited(issuanceNow.Add(15 * time.Hour)).Names[1].LastError}
	h.deps.Domain = fakeDomain{w: h.w, waitErr: errors.Join(errors.New("x"), refusal)}
	opts := h.opts(ip1, ip2)
	opts.Domain = "cluster.example.org"
	opts.ClusterOnly, opts.StorageGB = true, 0
	_, err := run(t, h, opts)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"has no certificate", "because of its rate limit", ip2 + " cannot join", "resumes at this step"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "is not delegated yet") {
		t.Errorf("the domain is delegated; the error must not say it is not:\n%v", err)
	}
}

// A node's answer is not trusted to be clean: control characters in any of its strings (the type,
// the message, the CA) do not reach the operator's terminal through the reason.
func TestClusterDomain_aNodesControlCharactersDoNotReachTheReason(t *testing.T) {
	status := rateLimited(issuanceNow.Add(15 * time.Hour))
	e := status.Names[1].LastError
	e.Type, e.Message, e.CA = "\x1b[2Jdns", "\x1b]0;owned\x07refused", "https://ca\x1b[31m.example"
	m, c, _ := issuanceCluster(status, nil)
	_, err := c.ready(context.Background(), m, issuanceDomain)
	if err == nil {
		t.Fatal("no reason")
	}
	if strings.ContainsAny(err.Error(), "\x1b\x07") {
		t.Fatalf("control characters reached the reason: %q", err.Error())
	}
}
