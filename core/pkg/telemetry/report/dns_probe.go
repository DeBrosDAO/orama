package report

import (
	"context"
	"crypto/tls"
	"math"
	"net"
	"time"

	"github.com/miekg/dns"
)

// The zone and certificate checks run in-process. They used to shell out to
// dig and openssl, which a node need not have installed (Debian 12 ships
// without dig), so on such a node every check read as failed; and openssl ran
// through bash with the wildcard name unquoted, where the shell could expand
// it as a glob.

var (
	// localDNSAddr is the nameserver on this host.
	localDNSAddr = "127.0.0.1:53"
	// localTLSAddr is Caddy on this host.
	localTLSAddr = "127.0.0.1:443"
)

const (
	// dnsCollectTimeout bounds the whole DNS section.
	dnsCollectTimeout = 20 * time.Second
	// dnsQueryTimeout bounds one DNS query.
	dnsQueryTimeout = 2 * time.Second
	// tlsProbeTimeout bounds one TLS handshake.
	tlsProbeTimeout = 4 * time.Second
	// wildcardProbeLabel is a name under the zone that only the wildcard
	// record and certificate cover.
	wildcardProbeLabel = "status-probe"
	// hoursPerDay converts a certificate's remaining lifetime to days.
	hoursPerDay = 24
)

// probeZone asks the local nameserver for the zone's SOA, NS, apex A and a
// wildcard-covered A record.
func probeZone(ctx context.Context, r *DNSReport, domain string) {
	r.SOAResolves = len(queryLocal(ctx, domain, dns.TypeSOA)) > 0
	ns := queryLocal(ctx, domain, dns.TypeNS)
	r.NSResolves, r.NSRecordCount = len(ns) > 0, len(ns)
	r.BaseAResolves = len(queryLocal(ctx, domain, dns.TypeA)) > 0
	r.WildcardResolves = len(queryLocal(ctx, wildcardProbeLabel+"."+domain, dns.TypeA)) > 0
}

// queryLocal returns the answer records of type qtype for name from the local
// nameserver. A failed query has no answers.
func queryLocal(ctx context.Context, name string, qtype uint16) []dns.RR {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(name), qtype)
	client := &dns.Client{Timeout: dnsQueryTimeout}
	qctx, cancel := context.WithTimeout(ctx, dnsQueryTimeout)
	defer cancel()
	resp, _, err := client.ExchangeContext(qctx, msg, localDNSAddr)
	if err != nil || resp == nil || resp.Rcode != dns.RcodeSuccess {
		return nil
	}
	var answers []dns.RR
	for _, rr := range resp.Answer {
		if rr.Header().Rrtype == qtype {
			answers = append(answers, rr)
		}
	}
	return answers
}

// tlsDaysLeft is how many whole days remain on the certificate Caddy serves
// for serverName, and whether it has expired; -1 when none could be read.
// Only the expiry is wanted: whether the certificate verifies is not this
// check's question (a staging CA's does not), so the chain is not verified.
func tlsDaysLeft(ctx context.Context, serverName string) (int, bool) {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: tlsProbeTimeout},
		Config:    &tls.Config{ServerName: serverName, InsecureSkipVerify: true}, //nolint:gosec // reads expiry only
	}
	dctx, cancel := context.WithTimeout(ctx, tlsProbeTimeout)
	defer cancel()
	conn, err := dialer.DialContext(dctx, "tcp", localTLSAddr)
	if err != nil {
		return -1, false
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return -1, false
	}
	left := time.Until(certs[0].NotAfter)
	if left <= 0 {
		return 0, true
	}
	return int(math.Floor(left.Hours() / hoursPerDay)), false
}
