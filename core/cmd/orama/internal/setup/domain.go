package setup

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
)

const (
	// httpsPort is where the cluster serves its certificate.
	httpsPort = "443"
	// certDialTimeout bounds one look at the cluster's certificate.
	certDialTimeout = 10 * time.Second
)

// clusterDomain handles a private cluster's own domain: the records its parent
// zone needs are read from the cluster (which nameserver slot holds which
// address is only known to it), and DNS is polled until the parent zone returns
// them and the cluster serves a certificate for the domain.
type clusterDomain struct {
	// Test seams.
	read     func(env string) ([]dnsdelegation.Delegation, error)
	check    func(ctx context.Context, d dnsdelegation.Delegation) ([]dnsdelegation.Finding, error)
	dialCert func(ctx context.Context, domain string) (*tls.ConnectionState, error)
}

func newClusterDomain() clusterDomain {
	return clusterDomain{
		read: dnsdelegation.Read,
		check: func(ctx context.Context, d dnsdelegation.Delegation) ([]dnsdelegation.Finding, error) {
			return dnsdelegation.Check(ctx, d, dnsdelegation.Lookups{})
		},
		dialCert: func(ctx context.Context, domain string) (*tls.ConnectionState, error) {
			return dialCertificate(ctx, domain, net.JoinHostPort(domain, httpsPort))
		},
	}
}

// delegationFor is the cluster's delegation of domain.
func (c clusterDomain) delegationFor(env, domain string) (dnsdelegation.Delegation, error) {
	all, err := c.read(env)
	if err != nil {
		return dnsdelegation.Delegation{}, err
	}
	for _, d := range all {
		if d.Domain == domain {
			return d, nil
		}
	}
	return dnsdelegation.Delegation{}, fmt.Errorf("the cluster %q has no nameserver for %s yet", env, domain)
}

// Records are the NS and glue records to create in the parent zone.
func (c clusterDomain) Records(_ context.Context, env, domain string) ([]string, error) {
	d, err := c.delegationFor(env, domain)
	if err != nil {
		return nil, err
	}
	return dnsdelegation.Records(d), nil
}

// Wait polls until the records resolve and the certificate is the cluster's.
func (c clusterDomain) Wait(ctx context.Context, env, domain string, poll, deadline time.Duration) error {
	return pollUntil(ctx, poll, deadline, domain+" to be delegated and to serve a certificate", func(ctx context.Context) (bool, error) {
		d, err := c.delegationFor(env, domain)
		if err != nil {
			return false, nil
		}
		findings, err := c.check(ctx, d)
		if err != nil || len(findings) > 0 {
			return false, nil
		}
		state, err := c.dialCert(ctx, domain)
		if err != nil {
			return false, nil
		}
		return certServes(state, domain, time.Now()), nil
	})
}

// dialCertificate reads the certificate the cluster serves for domain. The
// chain is not verified here: a cluster on a staging or private CA would never
// pass, and what is asked is whether the cluster has issued one for the name
// (certServes).
func dialCertificate(ctx context.Context, domain, addr string) (*tls.ConnectionState, error) {
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: certDialTimeout}, Config: &tls.Config{ServerName: domain, InsecureSkipVerify: true}}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	return &state, nil
}

// certServes says the connection's leaf certificate names domain (directly or
// by a wildcard of its parent), is unexpired, and is not self-signed: the
// fallback certificate a node serves before it has issued one is.
func certServes(state *tls.ConnectionState, domain string, now time.Time) bool {
	if state == nil || len(state.PeerCertificates) == 0 {
		return false
	}
	leaf := state.PeerCertificates[0]
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) || leaf.Issuer.String() == leaf.Subject.String() {
		return false
	}
	return leaf.VerifyHostname(domain) == nil
}
