package tlsstatus

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// Caddy's log lines as the node's journal holds them (-o cat), taken from the
// stagenet run of 2026-10-11 that failed with "tls: internal error".
const (
	rateLimitedLine = `{"level":"error","ts":1791692761.123456,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"stagenet.example.org","issuer":"acme-v02.api.letsencrypt.org-directory","error":"HTTP 429 urn:ietf:params:acme:error:rateLimited - too many certificates (5) already issued for this exact set of identifiers in the last 168h0m0s, retry after 2026-10-12 04:26:01 UTC: see https://letsencrypt.org/docs/rate-limits/#new-certificates-per-exact-set-of-identifiers"}`
	retryLine       = `{"level":"error","ts":1760155562.5,"logger":"tls.obtain","msg":"will retry","error":"[*.stagenet.example.org] Obtain: [*.stagenet.example.org] creating new order: attempt 1: https://acme-v02.api.letsencrypt.org/acme/new-order: HTTP 429 urn:ietf:params:acme:error:rateLimited - too many certificates (5) already issued for this exact set of identifiers in the last 168h0m0s, retry after 2026-10-12 04:26:01 UTC: see https://letsencrypt.org/docs/rate-limits/","attempt":2,"retrying_in":120,"elapsed":3.2,"max_duration":2592000}`
	dnsLine         = `{"level":"error","ts":1760155600,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"push.stagenet.example.org","issuer":"acme-v02.api.letsencrypt.org-directory","error":"HTTP 400 urn:ietf:params:acme:error:dns - DNS problem: SERVFAIL looking up TXT for _acme-challenge.push.stagenet.example.org"}`
	solverLine      = `{"level":"error","ts":1760155700,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"node-abc123.stagenet.example.org","issuer":"acme-v02.api.letsencrypt.org-directory","error":"[node-abc123.stagenet.example.org] solving challenge: node-abc123.stagenet.example.org: [node-abc123.stagenet.example.org] solver: orama dns: present returned 502"}`
	obtainedLine    = `{"level":"info","ts":1760159999.5,"logger":"tls.obtain","msg":"certificate obtained successfully","identifier":"stagenet.example.org","issuer":"acme-v02.api.letsencrypt.org-directory"}`
	renewFailedLine = `{"level":"error","ts":1760160000,"logger":"tls.renew","msg":"could not get certificate from issuer","identifier":"stagenet.example.org","issuer":"acme-v02.api.letsencrypt.org-directory","error":"HTTP 503 urn:ietf:params:acme:error:serverInternal - try again"}`
	infoLine        = `{"level":"info","ts":1760155500,"logger":"tls","msg":"finished cleaning storage units"}`
)

func journal(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestLastObtainErrors_rateLimited(t *testing.T) {
	got := lastObtainErrors(journal(infoLine, rateLimitedLine), "https://acme-v02.api.letsencrypt.org/directory")
	e, ok := got["stagenet.example.org"]
	if !ok || len(got) != 1 {
		t.Fatalf("got %+v, want one error for stagenet.example.org", got)
	}
	if !e.RateLimited() || e.Status != 429 || e.Type != "rateLimited" {
		t.Errorf("not read as a rate limit: %+v", e)
	}
	if want := time.Date(2026, 10, 12, 4, 26, 1, 0, time.UTC); !e.RetryAfter.Equal(want) {
		t.Errorf("retry after %v, want %v", e.RetryAfter, want)
	}
	if e.CA != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Errorf("CA %q, want the Caddyfile's directory", e.CA)
	}
	if !strings.HasPrefix(e.Message, "too many certificates (5) already issued") {
		t.Errorf("message %q lacks the CA's words", e.Message)
	}
	if e.At.UTC().Format(time.RFC3339) != "2026-10-11T04:26:01Z" {
		t.Errorf("at %v is not the log line's ts", e.At)
	}
}

// The retry line has no identifier field: the name is the one certmagic puts
// in brackets at the start of the error, the wildcard included.
func TestLastObtainErrors_nameFromTheErrorWhenNoIdentifier(t *testing.T) {
	got := lastObtainErrors(journal(retryLine), "")
	e, ok := got["*.stagenet.example.org"]
	if !ok {
		t.Fatalf("got %+v, want an error for the wildcard", got)
	}
	if !e.RateLimited() || e.RetryAfter.IsZero() {
		t.Errorf("the nested ACME problem was not found: %+v", e)
	}
}

func TestLastObtainErrors_otherACMEProblemsAndErrorsWithNone(t *testing.T) {
	got := lastObtainErrors(journal(dnsLine, solverLine), "")
	dns := got["push.stagenet.example.org"]
	if dns.Type != "dns" || dns.Status != 400 || dns.RateLimited() || !dns.RetryAfter.IsZero() {
		t.Errorf("dns problem: %+v", dns)
	}
	if !strings.HasPrefix(dns.Message, "DNS problem: SERVFAIL") {
		t.Errorf("dns message %q", dns.Message)
	}
	solver := got["node-abc123.stagenet.example.org"]
	if solver.Type != "" || solver.Status != 0 || !strings.Contains(solver.Message, "orama dns: present returned 502") {
		t.Errorf("an error with no ACME problem is kept whole: %+v", solver)
	}
	if solver.CA != "acme-v02.api.letsencrypt.org-directory" {
		t.Errorf("without a Caddyfile directory the CA is the issuer key the log names, got %q", solver.CA)
	}
}

func TestLastObtainErrors_aSuccessAfterAnErrorClearsIt(t *testing.T) {
	got := lastObtainErrors(journal(rateLimitedLine, obtainedLine), "")
	if len(got) != 0 {
		t.Errorf("got %+v, want none: the certificate was obtained after the failure", got)
	}
}

func TestLastObtainErrors_anErrorAfterASuccessIsKept(t *testing.T) {
	got := lastObtainErrors(journal(obtainedLine, renewFailedLine), "")
	e, ok := got["stagenet.example.org"]
	if !ok || e.Type != "serverInternal" || e.Status != 503 {
		t.Errorf("a failed renewal after an issue: %+v", got)
	}
}

func TestLastObtainErrors_theLastErrorWins(t *testing.T) {
	got := lastObtainErrors(journal(rateLimitedLine, renewFailedLine), "")
	if got["stagenet.example.org"].Type != "serverInternal" {
		t.Errorf("got %+v, want the later error", got)
	}
}

// A node that was rate limited by production and then pointed at staging still
// has production's errors in its journal; they are not staging's answer.
func TestLastObtainErrors_anotherCAsErrorsAreNotTheCurrentCAs(t *testing.T) {
	staging := "https://acme-staging-v02.api.letsencrypt.org/directory"
	if got := lastObtainErrors(journal(rateLimitedLine, retryLine), staging); len(got) != 0 {
		t.Errorf("got %+v, want none: both lines are production's", got)
	}
	stagingLine := strings.Replace(rateLimitedLine, `"issuer":"acme-v02.api.letsencrypt.org-directory"`, `"issuer":"acme-staging-v02.api.letsencrypt.org-directory"`, 1)
	if got := lastObtainErrors(journal(stagingLine), staging); len(got) != 1 {
		t.Errorf("got %+v, want staging's own error", got)
	}
}

func TestLastObtainErrors_noLog(t *testing.T) {
	for name, in := range map[string]string{
		"empty":      "",
		"no entries": "-- No entries --\n",
		"info only":  journal(infoLine),
	} {
		if got := lastObtainErrors(in, ""); len(got) != 0 {
			t.Errorf("%s: got %+v", name, got)
		}
	}
}

func TestLastObtainErrors_malformedLinesAreSkipped(t *testing.T) {
	in := journal(
		"not json at all",
		`{"level":"error","logger":"tls.obtain","msg":"could not get certif`, // truncated
		`{"level":"error","logger":"tls.obtain","msg":"x","error":"no name and no brackets"}`,
		`{"level":"error","logger":"tls.obtain","identifier":"a.example.org","error":""}`,
		`{"level":"error","logger":"http.log","identifier":"a.example.org","error":"not a certificate"}`,
		`{"level":"error","logger":"tls.obtain","error":"[unterminated"}`,
		`Oct 11 04:26:01 node orama-caddy-index[1]: `+dnsLine, // a journal prefix before the JSON
	)
	got := lastObtainErrors(in, "")
	if len(got) != 1 || got["push.stagenet.example.org"].Type != "dns" {
		t.Errorf("got %+v, want only the line with the journal prefix", got)
	}
}

func TestParseObtainError_noRetryTimeWhenTheCAGaveNone(t *testing.T) {
	status, problem, message, retry := parseObtainError("HTTP 429 urn:ietf:params:acme:error:rateLimited - slow down")
	if status != 429 || problem != "rateLimited" || message != "slow down" || !retry.IsZero() {
		t.Errorf("%d %q %q %v", status, problem, message, retry)
	}
}

// A CA's message is printed on a root terminal and in setup's error: an escape sequence in it, which
// the journal stores JSON-escaped and decoding turns back into control characters, is made harmless.
func TestLastObtainErrors_anEscapeSequenceInTheCAsWordsIsCleaned(t *testing.T) {
	line := strings.Replace(rateLimitedLine, "too many certificates", `\u001b]0;owned\u0007too many certificates`, 1)
	got := lastObtainErrors(journal(line), "")
	e, ok := got["stagenet.example.org"]
	if !ok {
		t.Fatalf("the error was not read: %+v", got)
	}
	if strings.ContainsAny(e.Message, "\x1b\x07") {
		t.Fatalf("control characters reached the message: %q", e.Message)
	}
	if !strings.Contains(e.Message, "?]0;owned?too many certificates") {
		t.Fatalf("the message lost its text: %q", e.Message)
	}
}

// The journal is filtered by certificateLoggerPattern before lastObtainErrors reads it: the pattern
// must take every line of the loggers the parser reads and none of another logger.
func TestCertificateLoggerPattern_selectsTheParsersLoggers(t *testing.T) {
	re := regexp.MustCompile(certificateLoggerPattern)
	for _, line := range []string{rateLimitedLine, `{"logger":"` + loggerRenew + `","msg":"x"}`, `{"logger":"` + loggerObtain + `"}`} {
		if !re.MatchString(line) {
			t.Errorf("the journal filter drops %.80s", line)
		}
	}
	for _, line := range []string{`{"logger":"http.log","msg":"http: TLS handshake error"}`, `{"logger":"tlsXobtain"}`} {
		if re.MatchString(line) {
			t.Errorf("the journal filter keeps %s", line)
		}
	}
}
