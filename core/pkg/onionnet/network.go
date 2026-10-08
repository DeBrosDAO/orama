// Package onionnet describes an Orama Tor network to a client and runs an
// unmodified upstream tor against it.
//
// A network is a file (network.json) that names the directory authorities, a
// fallback list and, optionally, validator onion services. The file is the
// whole contract between the network's builder (track E: dirauths, relays,
// validator onion services) and its clients (the VPN client and onion
// transaction submission): a client needs nothing else to join. The private
// stagenet network and, later, the public one differ only in this file.
//
// A client built here has one route: the tor it starts, configured with the
// network's authorities and no others. It never falls back to the public Tor
// network or to a direct connection.
package onionnet

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/chainonion"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// NetworkEnv is the variable that names the network file for every client
	// of a network: orama vpn (--network) and onion submission (--onion-network).
	NetworkEnv = "ORAMA_ONION_NETWORK"
	// DefaultTorBinary is the tor a client starts when none is named.
	DefaultTorBinary = "tor"

	// maxNetworkFile bounds a network file; a real one is a few kilobytes.
	maxNetworkFile = 1 << 20
	// maxListed bounds the authorities, fallbacks and onions in one file.
	maxListed = 1024
	hexIDLen  = 40
)

// nameRE is a nickname or network name: it goes into a torrc line and a
// directory name, so nothing but these characters is allowed.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,18}$`)

// Authority is one directory authority.
type Authority struct {
	Nickname string `json:"nickname"`
	// Address is the authority's IP address and directory port, "ip:dirport".
	Address string `json:"address"`
	ORPort  int    `json:"orport"`
	// V3Ident is the authority's v3 identity digest, 40 hex characters.
	V3Ident string `json:"v3ident"`
	// Fingerprint is the authority's relay identity digest, 40 hex characters.
	Fingerprint string `json:"fingerprint"`
}

// Fallback is a relay a client may fetch the first directory information from.
type Fallback struct {
	// Address is the relay's IP address and directory port, "ip:dirport".
	Address string `json:"address"`
	ORPort  int    `json:"orport"`
	// ID is the relay's identity digest, 40 hex characters.
	ID string `json:"id"`
}

// Network is one Tor network.
type Network struct {
	Name string `json:"name"`
	// Private must be true. A public network is not launched (track E, E7):
	// the client refuses a file that says otherwise.
	Private     bool        `json:"private"`
	Authorities []Authority `json:"authorities"`
	Fallbacks   []Fallback  `json:"fallbacks"`
	// ValidatorOnions are validator onion services that accept transaction
	// submissions, as "addr.onion[:port]". A submission picks one at random.
	ValidatorOnions []string `json:"validator_onions,omitempty"`
}

// ErrPublicNetwork is a network file that is not marked private.
var ErrPublicNetwork = errors.New("the public Orama Tor network is not launched; only a private network can be joined")

// Load reads and validates the network file at path.
func Load(path string) (Network, error) {
	f, err := os.Open(path)
	if err != nil {
		return Network{}, fmt.Errorf("open the Tor network file: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxNetworkFile+1))
	if err != nil {
		return Network{}, fmt.Errorf("read the Tor network file: %w", err)
	}
	if len(raw) > maxNetworkFile {
		return Network{}, fmt.Errorf("Tor network file %s is larger than %d bytes", path, maxNetworkFile)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var n Network
	if err := dec.Decode(&n); err != nil {
		return Network{}, fmt.Errorf("Tor network file %s: %w", path, err)
	}
	if err := n.Validate(); err != nil {
		return Network{}, fmt.Errorf("Tor network file %s: %w", path, err)
	}
	return n, nil
}

// Validate checks a network at the boundary: every field ends up in a torrc, so
// each is held to its exact form.
func (n Network) Validate() error {
	if !n.Private {
		return ErrPublicNetwork
	}
	if !nameRE.MatchString(n.Name) {
		return fmt.Errorf("network name %q must be 1-19 letters, digits, '-' or '_'", n.Name)
	}
	if len(n.Authorities) < tornet.MinAuthorities {
		return fmt.Errorf("the network lists %d authorities, it needs at least %d", len(n.Authorities), tornet.MinAuthorities)
	}
	if len(n.Authorities) > maxListed || len(n.Fallbacks) > maxListed || len(n.ValidatorOnions) > maxListed {
		return fmt.Errorf("a network lists at most %d entries of each kind", maxListed)
	}
	seen := map[string]bool{}
	for i, a := range n.Authorities {
		if err := a.validate(); err != nil {
			return fmt.Errorf("authority %d: %w", i, err)
		}
		if seen[a.V3Ident] {
			return fmt.Errorf("authority %d: v3ident %s is listed twice", i, a.V3Ident)
		}
		seen[a.V3Ident] = true
	}
	for i, f := range n.Fallbacks {
		if err := f.validate(); err != nil {
			return fmt.Errorf("fallback %d: %w", i, err)
		}
	}
	for i, o := range n.ValidatorOnions {
		if _, err := chainonion.Base(o); err != nil {
			return fmt.Errorf("validator onion %d: %w", i, err)
		}
	}
	return nil
}

func (a Authority) validate() error {
	if !nameRE.MatchString(a.Nickname) {
		return fmt.Errorf("nickname %q must be 1-19 letters, digits, '-' or '_'", a.Nickname)
	}
	if err := validAddrPort(a.Address); err != nil {
		return err
	}
	if err := validPort(a.ORPort, "orport"); err != nil {
		return err
	}
	if err := validHexID(a.V3Ident, "v3ident"); err != nil {
		return err
	}
	return validHexID(a.Fingerprint, "fingerprint")
}

func (f Fallback) validate() error {
	if err := validAddrPort(f.Address); err != nil {
		return err
	}
	if err := validPort(f.ORPort, "orport"); err != nil {
		return err
	}
	return validHexID(f.ID, "id")
}

// canonIPv4Port accepts an IPv4 literal and a port and returns the canonical
// form to write into a torrc. A name would have the client ask a resolver where
// to find the network before it has joined it. IPv6 is not accepted: v1 relays
// are IPv4 only, and netip accepts any text after a '%' zone, newlines
// included, which would reach the torrc.
func canonIPv4Port(s string) (string, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return "", fmt.Errorf("address %q must be ip:port with an IPv4 literal: %w", s, err)
	}
	if !ap.Addr().Is4() || ap.Port() == 0 {
		return "", fmt.Errorf("address %q must be an IPv4 literal with a port from 1 to 65535", s)
	}
	return ap.String(), nil
}

func validAddrPort(s string) error {
	_, err := canonIPv4Port(s)
	return err
}

func validPort(p int, what string) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%s %d is not a port", what, p)
	}
	return nil
}

func validHexID(s, what string) error {
	b, err := hex.DecodeString(s)
	if err != nil || len(s) != hexIDLen || len(b)*2 != hexIDLen {
		return fmt.Errorf("%s %q must be %d hex characters", what, s, hexIDLen)
	}
	return nil
}

// ErrNoValidatorOnion means the network file lists no validator onion service
// to submit a transaction to.
var ErrNoValidatorOnion = errors.New("the Tor network file lists no validator onion service; pass --onion")

// RandomValidatorOnion picks one of the network's validator onion services
// uniformly with a cryptographic random source, so no transaction is steered
// to a fixed validator.
func (n Network) RandomValidatorOnion() (string, error) {
	if len(n.ValidatorOnions) == 0 {
		return "", ErrNoValidatorOnion
	}
	i, err := rand.Int(rand.Reader, big.NewInt(int64(len(n.ValidatorOnions))))
	if err != nil {
		return "", fmt.Errorf("pick a validator onion: %w", err)
	}
	return n.ValidatorOnions[i.Int64()], nil
}

// DefaultDataDir is where a client keeps tor's state for the network: under
// the user's cache directory, so the consensus and the guards survive between
// runs and each run does not fetch the consensus again.
func DefaultDataDir(name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("network name %q is not a directory name", name)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find the user cache directory: %w", err)
	}
	return filepath.Join(cache, "orama", "onion", name), nil
}

// ValidateLoopback checks a listen address a client binds: an IP loopback
// literal without a zone and a numeric port, never a name or another host.
func ValidateLoopback(addr string) error {
	_, err := canonLoopback(addr)
	return err
}

// canonLoopback validates addr as ValidateLoopback does and returns the form
// to write into a torrc.
func canonLoopback(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%q must be host:port: %w", addr, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() || ip.Zone() != "" {
		return "", fmt.Errorf("%q: %q is not a loopback IP address", addr, host)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("%q: %q is not a port", addr, port)
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(p)), nil
}
