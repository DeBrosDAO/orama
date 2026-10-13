// Package tlsstatus reports the certificate issuance of the node on this
// machine: for each name its Caddy manages, whether a certificate is held and,
// when it is not, why the certificate authority last refused.
//
// The answer is in two places, and neither is Caddy's admin API, which does not
// expose it. What Caddy holds is read the way a client meets it, by a handshake
// with the local listener. Why it holds nothing is in Caddy's own JSON log, in
// the journal of its unit.
package tlsstatus

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/termtext"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

const (
	// CaddyUnit is the unit of the node's Caddy.
	CaddyUnit = "orama-namespace-caddy@index"
	// CaddyfilePath is the Caddyfile that unit runs.
	CaddyfilePath = "/etc/caddy/Caddyfile"

	// journalLines is how much of the journal is read: the most the privileged
	// journal reader returns in one call.
	journalLines = privhelper.MaxJournalLines
	// handshakeTimeout bounds one look at a certificate.
	handshakeTimeout = 5 * time.Second
	// wildcardProbeLabel is a name under a wildcard that only the wildcard's
	// certificate covers.
	wildcardProbeLabel = "tls-status"
)

// Report is the certificate issuance of one node.
type Report struct {
	// Unit is the Caddy unit whose journal was read.
	Unit string `json:"unit"`
	// CA is the ACME directory the node's Caddyfile names.
	CA string `json:"ca,omitempty"`
	// Names are the names Caddy manages, in Caddyfile order.
	Names []Name `json:"names"`
}

// Name is the certificate of one name.
type Name struct {
	Name string `json:"name"`
	// Held says Caddy serves an issued, unexpired certificate for the name.
	Held bool `json:"held"`
	// Issuer and NotAfter describe the certificate held.
	Issuer   string    `json:"issuer,omitempty"`
	NotAfter time.Time `json:"not_after,omitzero"`
	// ServeError is why the handshake gave no certificate, when it did not.
	ServeError string `json:"serve_error,omitempty"`
	// LastError is the last failure to obtain or renew the certificate that no
	// success followed in the journal read.
	LastError *ObtainError `json:"last_error,omitempty"`
}

// Deps are the node's facts, replaced in tests.
type Deps struct {
	// ReadFile reads the Caddyfile.
	ReadFile func(path string) ([]byte, error)
	// Journal returns the last lines of a unit's journal.
	Journal func(ctx context.Context, unit string, lines int) (string, error)
	// Handshake presents serverName to addr and returns what the server serves.
	Handshake func(ctx context.Context, addr, serverName string) (*tls.ConnectionState, error)
	Now       func() time.Time
}

// SystemDeps are the real ones.
func SystemDeps() Deps {
	return Deps{ReadFile: os.ReadFile, Journal: readJournal, Handshake: handshake, Now: time.Now}
}

// Collect reads this node's certificate issuance.
func Collect(ctx context.Context, d Deps) (*Report, error) {
	raw, err := d.ReadFile(CaddyfilePath)
	if err != nil {
		return nil, fmt.Errorf("read the Caddyfile %s (is this node installed?): %w", CaddyfilePath, err)
	}
	cf := parseCaddyfile(string(raw))
	journal, err := d.Journal(ctx, CaddyUnit, journalLines)
	if err != nil {
		return nil, fmt.Errorf("read the journal of %s for the CA's answers: %w", CaddyUnit, err)
	}
	failures := lastObtainErrors(journal, cf.ACMECA)

	r := &Report{Unit: CaddyUnit, CA: termtext.Clean(cf.ACMECA), Names: make([]Name, 0, len(cf.Names))}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cf.HTTPSPort))
	for _, name := range cf.Names {
		n := Name{Name: termtext.Clean(name)}
		if f, ok := failures[name]; ok {
			n.LastError = &f
		}
		state, err := d.Handshake(ctx, addr, probeName(name))
		if err != nil {
			n.ServeError = termtext.Clean(err.Error())
		} else {
			n.Issuer, n.NotAfter, n.Held = held(state, probeName(name), d.Now())
			n.Issuer = termtext.Clean(n.Issuer)
		}
		r.Names = append(r.Names, n)
	}
	return r, nil
}

// probeName is the name presented to Caddy to ask for the certificate of site:
// the site itself, or for a wildcard a name under it.
func probeName(site string) string {
	if rest, ok := strings.CutPrefix(site, "*."); ok {
		return wildcardProbeLabel + "." + rest
	}
	return site
}

// held says the connection's leaf certificate names serverName, is unexpired
// and is not self-signed, and returns who issued it and when it expires.
func held(state *tls.ConnectionState, serverName string, now time.Time) (issuer string, notAfter time.Time, ok bool) {
	if state == nil || len(state.PeerCertificates) == 0 {
		return "", time.Time{}, false
	}
	leaf := state.PeerCertificates[0]
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) || leaf.Issuer.String() == leaf.Subject.String() {
		return "", time.Time{}, false
	}
	if leaf.VerifyHostname(serverName) != nil {
		return "", time.Time{}, false
	}
	return firstNonEmpty(leaf.Issuer.CommonName, leaf.Issuer.String()), leaf.NotAfter.UTC(), true
}

// handshake reads the certificate the server at addr presents for serverName.
// The chain is not verified: a staging or private CA's never would, and the
// question is whether the node has one issued for the name.
func handshake(ctx context.Context, addr, serverName string) (*tls.ConnectionState, error) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: handshakeTimeout},
		Config:    &tls.Config{ServerName: serverName, InsecureSkipVerify: true}, //nolint:gosec // reads what is served, not whether it is trusted
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	return &state, nil
}

// readJournal reads the last lines of unit's journal that the certificate loggers wrote, oldest
// first, as the message alone, which for Caddy is its JSON log line. The window must hold the CA's
// answers however many other lines the unit logs: a node with no certificate logs a handshake error
// per client, which pushed the last failed order out of a window of the unit's last lines. So
// journalctl reads backwards from the newest entry and counts only the lines --grep keeps
// (--reverse: systemd 255 implies it for --grep with -n, 249 and 252 count before they filter
// without it), and the lines are put back in order. It needs root, like `orama node logs`.
func readJournal(ctx context.Context, unit string, lines int) (string, error) {
	cmd := exec.CommandContext(ctx, "journalctl", journalArgs(unit, lines)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	newestFirst, err := journalResult(unit, out, stderr.Bytes(), err)
	if err != nil {
		return "", err
	}
	return oldestFirst(newestFirst), nil
}

// oldestFirst reverses journal lines read newest first: lastObtainErrors reads them in the order
// they were written, so a success after a failure clears it and a failure after a success stands.
func oldestFirst(newestFirst string) string {
	lines := strings.Split(strings.TrimRight(newestFirst, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}
	slices.Reverse(lines)
	return strings.Join(lines, "\n") + "\n"
}

// journalArgs are journalctl's arguments for the certificate loggers' last lines of unit.
func journalArgs(unit string, lines int) []string {
	return []string{"-u", unit, "--no-pager", "-o", "cat", "--reverse", "--grep", certificateLoggerPattern, "-n", strconv.Itoa(lines)}
}

// journalResult reads journalctl's answer. It exits 1 when no entry matches, with nothing on
// stderr and on stdout nothing or, on a terminal and some versions, "-- No entries --": no answer
// from the CA is in the journal, which is a result, not a failure. Any other failure is one.
func journalResult(unit string, out, stderr []byte, err error) (string, error) {
	if err == nil {
		return string(out), nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return "", fmt.Errorf("journalctl -u %s: %w", unit, err)
	}
	stdout := strings.TrimSpace(string(out))
	if exit.ExitCode() == journalNoMatchExit && len(bytes.TrimSpace(stderr)) == 0 && (stdout == "" || stdout == journalNoEntries) {
		return "", nil
	}
	return "", fmt.Errorf("journalctl -u %s: %w: %s", unit, err, strings.TrimSpace(string(stderr)))
}

// journalNoEntries is what journalctl prints in place of entries when none match.
const journalNoEntries = "-- No entries --"

// certificateLoggerPattern selects the lines of the loggers that obtain and renew certificates
// (journal.go loggerObtain, loggerRenew) in the journal's message field.
const certificateLoggerPattern = `"logger":"tls\.(obtain|renew)"`

// journalNoMatchExit is journalctl's exit status when --grep matches no entry.
const journalNoMatchExit = 1
