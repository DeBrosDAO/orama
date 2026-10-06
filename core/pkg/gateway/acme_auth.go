package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"go.uber.org/zap"
)

// The DNS-01 endpoints publish a TXT record in the zone this cluster's
// nameservers serve, and a TXT record at _acme-challenge.<name> is what a CA
// accepts as proof of control of <name>. Whoever can call them can be issued a
// certificate for any name under the cluster's domain.
//
// They used to require only that the request reached the gateway from loopback
// with no forwarding header — which every process on the node does, a tenant's
// deployment included — and they wrote whatever fqdn the body named. They now
// require a MAC made with ACMEChallengeKey — HKDF of the cluster secret,
// which install writes for Caddy and the gateway derives again — over the
// request body, and they write only _acme-challenge records under the base domain.

const (
	// acmeChallengePrefix is the label every DNS-01 record name starts with
	// (RFC 8555 §8.4).
	acmeChallengePrefix = "_acme-challenge."

	// acmeRequestMaxBytes bounds a present or cleanup body.
	acmeRequestMaxBytes = 1 << 20
)

var (
	// acmeTXTValue is a DNS-01 record value: base64url, unpadded, of a SHA-256
	// digest — 43 characters (RFC 8555 §8.4). Anything else is not a
	// challenge answer.
	acmeTXTValue = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

	// dnsLabel is one label of a host name below the challenge label.
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// acmeChallengeRequest authenticates and decodes a present or cleanup call.
// It writes the refusal itself; ok is false when it has.
//
// An unauthenticated caller gets 404, as before: the endpoint should not
// confirm to anyone else that it exists.
func (g *Gateway) acmeChallengeRequest(w http.ResponseWriter, r *http.Request) (ACMERequest, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return ACMERequest{}, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, acmeRequestMaxBytes))
	if err != nil {
		g.logger.Error("Failed to read an ACME challenge request", zap.Error(err))
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return ACMERequest{}, false
	}
	if !g.verifyACMECaller(r, body) {
		http.Error(w, "not found", http.StatusNotFound)
		return ACMERequest{}, false
	}

	var req ACMERequest
	if err := json.Unmarshal(body, &req); err != nil {
		g.logger.Error("Failed to decode an ACME challenge request", zap.Error(err))
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return ACMERequest{}, false
	}

	fqdn, err := acmeChallengeFQDN(req.FQDN, g.baseDomain())
	if err != nil {
		g.logger.Warn("refused an ACME challenge record", zap.String("fqdn", req.FQDN), zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return ACMERequest{}, false
	}
	if !acmeTXTValue.MatchString(req.Value) {
		http.Error(w, "value is not a DNS-01 challenge answer (43 base64url characters)", http.StatusBadRequest)
		return ACMERequest{}, false
	}
	req.FQDN = fqdn
	return req, true
}

// verifyACMECaller reports whether r carries a MAC made with this cluster's
// ACME challenge key over body. The source address is not consulted.
func (g *Gateway) verifyACMECaller(r *http.Request, body []byte) bool {
	if g.cfg == nil {
		return false
	}
	key, err := nodeauth.ACMEChallengeKey(g.cfg.ClusterSecret)
	if err != nil {
		return false
	}
	return nodeauth.VerifyACME(key, r, body, time.Now())
}

// baseDomain is the zone this cluster's nameservers serve, or "" when this
// gateway has none configured.
func (g *Gateway) baseDomain() string {
	if g.cfg == nil {
		return ""
	}
	return g.cfg.BaseDomain
}

// acmeChallengeFQDN normalises name to the lower-case, dot-terminated form
// dns_records holds, and refuses anything that is not an _acme-challenge
// record for baseDomain or a name under it.
func acmeChallengeFQDN(name, baseDomain string) (string, error) {
	zone := strings.ToLower(strings.Trim(strings.TrimSpace(baseDomain), "."))
	if zone == "" {
		return "", fmt.Errorf("this gateway has no base domain, so it serves no zone to publish a challenge in")
	}
	fqdn := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	host, ok := strings.CutPrefix(fqdn, acmeChallengePrefix)
	if !ok {
		return "", fmt.Errorf("fqdn %q is not an %s record", name, strings.TrimSuffix(acmeChallengePrefix, "."))
	}
	if host != zone && !strings.HasSuffix(host, "."+zone) {
		return "", fmt.Errorf("fqdn %q is not under %s, the zone this cluster serves", name, zone)
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return "", fmt.Errorf("fqdn %q has a label that is not a host name label: %q", name, label)
		}
	}
	return fqdn + ".", nil
}
