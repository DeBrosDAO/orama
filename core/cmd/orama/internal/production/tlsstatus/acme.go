package tlsstatus

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ACMEProblemRateLimited is the ACME problem type (RFC 8555, the part after
// "urn:ietf:params:acme:error:") a CA answers when it refuses to issue because
// an account, an address or a set of names has used its allowance.
const ACMEProblemRateLimited = "rateLimited"

// ObtainError is the last time Caddy failed to obtain or renew the certificate
// for one name, as Caddy's own log says it.
type ObtainError struct {
	// At is when Caddy logged it.
	At time.Time `json:"at,omitzero"`
	// CA is the ACME directory the node asked, from its Caddyfile.
	CA string `json:"ca,omitempty"`
	// Status is the HTTP status of the CA's answer, 0 when the failure was not
	// the CA's answer (the DNS-01 challenge could not be set, the CA unreachable).
	Status int `json:"status,omitempty"`
	// Type is the ACME problem type, for example rateLimited. Empty when the CA
	// gave none.
	Type string `json:"type,omitempty"`
	// Message is what the CA said, or the error Caddy logged when it said nothing
	// in ACME terms.
	Message string `json:"message"`
	// RetryAfter is when the CA said it would accept the request again, when it
	// said so.
	RetryAfter time.Time `json:"retry_after,omitzero"`
}

// RateLimited says the CA refused because of a rate limit.
func (e ObtainError) RateLimited() bool { return e.Type == ACMEProblemRateLimited }

var (
	// acmeProblem matches the problem document certmagic puts in an error:
	// "HTTP 429 urn:ietf:params:acme:error:rateLimited - too many certificates ...".
	acmeProblem = regexp.MustCompile(`HTTP (\d{3}) urn:ietf:params:acme:error:([A-Za-z]+) - (.*)$`)
	// retryAfter matches the time Let's Encrypt names in a rate limit's detail.
	retryAfter = regexp.MustCompile(`retry after (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) UTC`)
)

const retryAfterLayout = "2006-01-02 15:04:05"

// parseObtainError reads the error text Caddy logged for a failed order into
// the ACME problem it carries. Text that carries none is kept whole as the
// message.
func parseObtainError(text string) (status int, problem, message string, retry time.Time) {
	text = strings.TrimSpace(text)
	m := acmeProblem.FindStringSubmatch(text)
	if m == nil {
		return 0, "", text, time.Time{}
	}
	status, _ = strconv.Atoi(m[1])
	message = strings.TrimSpace(m[3])
	if r := retryAfter.FindStringSubmatch(message); r != nil {
		if t, err := time.Parse(retryAfterLayout, r[1]); err == nil {
			retry = t.UTC()
		}
	}
	return status, m[2], message, retry
}
