package onionnet

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Options say where a client tor keeps its state and listens.
type Options struct {
	// DataDir is tor's DataDirectory: the cached consensus and the guards. It
	// must be an absolute path with no whitespace or newline in it.
	DataDir string
	// SocksAddr is the loopback "ip:port" tor accepts SOCKS5 on.
	SocksAddr string
	// DNSAddr, when set, is the loopback "ip:port" tor answers DNS on through
	// the network, for an application that must resolve names itself.
	DNSAddr string
}

// canon validates the options and returns the data directory and the listen
// addresses in the form written into the torrc.
func (o Options) canon() (dir, socks, dns string, err error) {
	if !filepath.IsAbs(o.DataDir) || strings.ContainsAny(o.DataDir, " \t\r\n\"\\#") {
		return "", "", "", fmt.Errorf("tor data directory %q must be an absolute path without spaces or special characters", o.DataDir)
	}
	if socks, err = canonLoopback(o.SocksAddr); err != nil {
		return "", "", "", fmt.Errorf("SOCKS address: %w", err)
	}
	if o.DNSAddr != "" {
		if dns, err = canonLoopback(o.DNSAddr); err != nil {
			return "", "", "", fmt.Errorf("DNS address: %w", err)
		}
	}
	return o.DataDir, socks, dns, nil
}

// Torrc renders the configuration of a client tor for the network: a client
// only, with exactly the network's authorities and fallbacks, no public Tor
// fallback list, no control port, and a listener on loopback. Every value is
// written in the canonical form it was parsed to, never as it was given.
func (n Network) Torrc(o Options) (string, error) {
	if err := n.Validate(); err != nil {
		return "", err
	}
	dir, socks, dns, err := o.canon()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Orama Tor network %s. Written for one run; do not edit.\n", n.Name)
	fmt.Fprintf(&b, "DataDirectory %s\n", dir)
	fmt.Fprintf(&b, "SocksPort %s IsolateSOCKSAuth\n", socks)
	if dns != "" {
		fmt.Fprintf(&b, "DNSPort %s\n", dns)
	}
	b.WriteString("ControlPort 0\nClientOnly 1\nORPort 0\nDirPort 0\nExitRelay 0\nClientRejectInternalAddresses 1\n")
	b.WriteString("UseDefaultFallbackDirs 0\nUseBridges 0\nSafeLogging 1\nLog notice stdout\n")
	for _, a := range n.Authorities {
		addr, err := canonIPv4Port(a.Address)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "DirAuthority %s orport=%d v3ident=%s %s %s\n", a.Nickname, a.ORPort, a.V3Ident, addr, a.Fingerprint)
	}
	for _, f := range n.Fallbacks {
		addr, err := canonIPv4Port(f.Address)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "FallbackDir %s orport=%d id=%s\n", addr, f.ORPort, f.ID)
	}
	return b.String(), nil
}
