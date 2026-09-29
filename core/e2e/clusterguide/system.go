package clusterguide

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// OramaExecutor runs the orama binary at Bin in place of the guide's "orama".
// Output is returned combined.
type OramaExecutor struct {
	Bin string
}

// Run executes argv. "rw" is refused: the fixture provides RootWallet vault
// entries, and the harness never types a secret.
func (e OramaExecutor) Run(ctx context.Context, argv []string) (string, error) {
	if argv[0] != "orama" {
		return "", fmt.Errorf("the harness runs only orama, not %q", argv[0])
	}
	cmd := exec.CommandContext(ctx, e.Bin, argv[1:]...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// ScanHostKey reads the SSH host-key fingerprint (SHA256:...) a machine
// presents now, with ssh-keyscan and ssh-keygen. It is trust on first use, of
// a machine the operator provisioned for this run.
func ScanHostKey(ip string) (string, error) {
	scan, err := exec.Command("ssh-keyscan", "-T", "10", "-t", "ed25519", ip).Output()
	if err != nil {
		return "", fmt.Errorf("ssh-keyscan %s: %w", ip, err)
	}
	if !strings.Contains(string(scan), "ssh-ed25519") {
		return "", fmt.Errorf("%s offered no ed25519 host key", ip)
	}
	gen := exec.Command("ssh-keygen", "-lf", "-")
	gen.Stdin = bytes.NewReader(scan)
	out, err := gen.Output()
	if err != nil {
		return "", fmt.Errorf("ssh-keygen -lf: %w", err)
	}
	for _, field := range strings.Fields(string(out)) {
		if strings.HasPrefix(field, "SHA256:") {
			return field, nil
		}
	}
	return "", fmt.Errorf("no SHA256 fingerprint in %q", strings.TrimSpace(string(out)))
}

// publicResolver is where LookupNS asks: a machine's own resolver may cache
// or not know the zone yet.
const publicResolver = "8.8.8.8:53"

// LookupNS returns the NS records a public resolver has for domain.
func LookupNS(domain string) ([]string, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, publicResolver)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records, err := r.LookupNS(ctx, domain)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ns := range records {
		out = append(out, ns.Host)
	}
	return out, nil
}

// CertServed checks that domain:443 completes a TLS handshake whose
// certificate names it. It does not verify the chain: a test cluster uses the
// Let's Encrypt staging CA, which no system trusts.
func CertServed(domain string) error {
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(domain, "443"), &tls.Config{ServerName: domain, InsecureSkipVerify: true}) //nolint:gosec // staging CA; only the presence of a certificate for the name is checked
	if err != nil {
		return err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fmt.Errorf("%s presented no certificate", domain)
	}
	if err := certs[0].VerifyHostname(domain); err != nil {
		return fmt.Errorf("the certificate %s serves does not name it: %w", domain, err)
	}
	return nil
}
