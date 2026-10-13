package setup

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/tlsstatus"
)

const (
	// stagingACMECA is the --acme-ca value that issues from Let's Encrypt staging.
	stagingACMECA = "letsencrypt-staging"
	// stagingTrustDoc is where the CLI's trust of a staging cluster is explained.
	stagingTrustDoc = "https://orama.network/docs/operator/tls-certificates#trusting-a-staging-cluster-from-your-cli"
)

// issuanceRefused is the certificate authority's reason a cluster has no
// certificate for its domain, read from the node's own log
// (`orama node tls status`). The client's only symptom is "tls: internal
// error", which names nothing.
type issuanceRefused struct {
	Domain string
	// Name is the name the CA refused: the domain or its wildcard.
	Name string
	Err  tlsstatus.ObtainError
	// Now is when the status was read.
	Now time.Time
}

func (e *issuanceRefused) Error() string {
	if !e.Err.RateLimited() {
		return fmt.Sprintf("the certificate authority refused to issue a certificate for %s: %s", e.Name, e.Err.Describe())
	}
	msg := fmt.Sprintf("the certificate authority refused to issue a certificate for %s because of its rate limit: %s", e.Name, e.Err.Message)
	if !e.Err.RetryAfter.IsZero() {
		msg += fmt.Sprintf("\n  It accepts the order again after %s", e.Err.RetryAfter.Format("2006-01-02 15:04:05 UTC"))
		if wait := e.Err.RetryAfter.Sub(e.Now); wait > 0 {
			msg += fmt.Sprintf(" (in %s)", wait.Round(time.Minute))
		}
		msg += "."
	}
	return msg + fmt.Sprintf("\n  A test network that is rebuilt often should be created with `--acme-ca %s`, whose limits are far higher. "+
		"Staging certificates are trusted by no client: give the CLI Let's Encrypt's staging roots with `orama network add <name> <url> --ca-file <roots.pem>` (%s).",
		stagingACMECA, stagingTrustDoc)
}

// whyNoCertificate adds to why, the dial's reason, what the node says about
// the certificate authority's answer. When the CA refused to issue, the reason
// is an *issuanceRefused; when the node cannot say, why carries that too, so
// the final error still names what could not be read.
func (c clusterDomain) whyNoCertificate(ctx context.Context, via Machine, domain string, why error) error {
	status, err := via.CertificateStatus(ctx)
	if err != nil {
		return fmt.Errorf("%w (the node's certificate status could not be read: %v)", why, err)
	}
	name, last := lastRefusal(status, domain)
	if last == nil {
		return why
	}
	// Every string is the node's, from a CA's answer: none of them can drive the terminal.
	refusal := *last
	refusal.Message, refusal.Type, refusal.CA = CleanTerminal(refusal.Message), CleanTerminal(refusal.Type), CleanTerminal(refusal.CA)
	return fmt.Errorf("%w: %w", why, &issuanceRefused{Domain: domain, Name: CleanTerminal(name), Err: refusal, Now: c.now()})
}

// lastRefusal is the failed order that explains a cluster with no certificate
// for domain: the failure of the domain or of its wildcard, a rate limit before
// any other.
func lastRefusal(status *tlsstatus.Report, domain string) (string, *tlsstatus.ObtainError) {
	var name string
	var found *tlsstatus.ObtainError
	for _, n := range status.Names {
		if n.Held || n.LastError == nil || (n.Name != domain && n.Name != "*."+domain) {
			continue
		}
		if found == nil || (n.LastError.RateLimited() && !found.RateLimited()) {
			name, found = n.Name, n.LastError
		}
	}
	return name, found
}

func (c clusterDomain) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}
