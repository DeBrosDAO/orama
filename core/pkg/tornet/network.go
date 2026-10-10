// Package tornet describes and configures the Orama Tor network: a separate
// anonymity network built from unmodified upstream Tor code and run by Orama's
// own directory authorities (docs/TOR_NETWORK.md).
//
// It renders torrc files for the five roles (directory authority, relay, exit,
// validator onion service, client), runs the offline authority key ceremony
// through the upstream tor and tor-gencert binaries, reads a consensus, and
// archives the votes and consensus a directory authority holds. It starts no
// process and opens no socket. The node's own Tor client
// (orama-namespace-tor@index, pkg/constants/tor.go) is a different thing: it
// stays on the public Tor network.
package tornet

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/chainonion"
	"github.com/DeBrosOfficial/network/pkg/netguard"
)

const (
	// MinAuthorities is the smallest directory-authority set. Fewer than three
	// cannot lose one and still hold a majority.
	MinAuthorities = 3
	// CertMonths is the lifetime of an authority's signing certificate.
	CertMonths = 12

	// NetworkFileLimit bounds a network file read from disk.
	NetworkFileLimit = 1 << 20
	// NetworkEnv is the variable that names the network file for every client
	// of a network: orama vpn (--network), onion transaction submission
	// (--onion-network) and the relay reporter (--network).
	NetworkEnv = "ORAMA_ONION_NETWORK"
	// maxValidatorOnions bounds the validator onion services one file lists.
	maxValidatorOnions = 1024

	// minVotingIntervalMinutes is Tor's own floor for a production voting interval.
	minVotingIntervalMinutes = 5
	// sharedRandomRounds is how many voting rounds one shared-random protocol run
	// takes: SHARED_RANDOM_N_ROUNDS (12) times SHARED_RANDOM_N_PHASES (2) in Tor's
	// shared_random_client.h. A run lasts that many voting intervals.
	sharedRandomRounds = 24
	// maxHSDirIntervalMinutes is HS_TIME_PERIOD_LENGTH_MAX in Tor's hs_common.h:
	// the longest time period an authority can ask clients to use.
	maxHSDirIntervalMinutes = 60 * 24 * 10
	// minDelaySeconds is Tor's floor for V3AuthVoteDelay and V3AuthDistDelay.
	minDelaySeconds = 20
	minutesPerDay   = 24 * 60
	// maxHSDirUptimeHours is Tor's own default: asking for more than the default is a typo.
	maxHSDirUptimeHours = 96

	fingerprintLen   = 40
	ed25519IDLen     = 43
	maxNicknameLen   = 19
	maxPort          = 65535
	maxNameLen       = 48
	maxContactLength = 200
)

var (
	nicknamePattern    = regexp.MustCompile(`^[A-Za-z0-9]{1,19}$`)
	networkNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}$`)
)

// ValidNetworkName reports whether name is a network name: it is also the
// name of a directory, so nothing but lower-case letters, digits and dashes.
func ValidNetworkName(name string) bool { return networkNamePattern.MatchString(name) }

// Authority is one directory authority as every relay and client knows it.
// Nothing in it is secret.
type Authority struct {
	Nickname string `json:"nickname"`
	// Address is the authority's public IPv4 address.
	Address string `json:"address"`
	ORPort  int    `json:"or_port"`
	DirPort int    `json:"dir_port"`
	// V3Ident is the SHA-1 digest (40 hex) of the authority identity key
	// tor-gencert made. It signs the authority's certificates and stays offline.
	V3Ident string `json:"v3_ident"`
	// Fingerprint is the SHA-1 digest (40 hex) of the authority's relay RSA
	// identity key, the one a relay descriptor and a DirAuthority line name.
	Fingerprint string `json:"fingerprint"`
	// Ed25519ID is the unpadded base64 of the relay's ed25519 master identity.
	Ed25519ID string `json:"ed25519_id"`
}

// ErrPublicNetwork is a network file that is not marked private.
var ErrPublicNetwork = errors.New("the public Orama Tor network is not launched; only a private network can be joined")

// ErrNoValidatorOnion means the network file lists no validator onion service
// to submit a transaction to.
var ErrNoValidatorOnion = errors.New("the Tor network file lists no validator onion service; pass --onion")

// Network is the description every relay and client of the Orama Tor network
// is given: its name, voting schedule, directory authorities and validator
// onion services. It is the one network file (tor-network.json): the ceremony
// writes it, the roles install from it and the clients (orama vpn, onion
// transaction submission, the relay reporter) join with it. Every authority
// must be configured with the same schedule or they cannot agree on a
// consensus.
type Network struct {
	Name string `json:"name"`
	// Private must be true. A public network is not launched (track E, E7): a
	// file that says otherwise is refused by every role and client.
	Private bool `json:"private"`
	// Bootstrap sets AssumeReachable on authorities and relays. A new network
	// needs it for its first consensus (Tor's manual: "used when bootstrapping a
	// new Tor network"); it bypasses reachability testing, so it is switched off
	// once the first consensus is signed.
	Bootstrap bool `json:"bootstrap"`
	// AllowExit lets nodes of this network be installed with the exit role. It is
	// true only in the network file of a network whose owner decided to run exits.
	AllowExit bool `json:"allow_exit"`
	// AllowSharedSubnets lets a circuit use two relays of one /16
	// (EnforceDistinctSubnets 0). A network with fewer /16 networks than a
	// circuit has hops cannot build one otherwise; it weakens path selection, so
	// only a small network sets it.
	AllowSharedSubnets bool `json:"allow_shared_subnets"`
	// HSDirMinUptimeHours is how long a relay must have been up before the
	// authorities give it the HSDir flag, which a validator's onion service needs
	// to publish. Zero keeps Tor's default of 96 hours, which a new network
	// cannot wait for.
	HSDirMinUptimeHours   int         `json:"hsdir_min_uptime_hours"`
	VotingIntervalMinutes int         `json:"voting_interval_minutes"`
	VoteDelaySeconds      int         `json:"vote_delay_seconds"`
	DistDelaySeconds      int         `json:"dist_delay_seconds"`
	Authorities           []Authority `json:"authorities"`
	// ValidatorOnions are the validator onion services that accept transaction
	// submissions, as "addr.onion[:port]". A submission picks one at random.
	// A validator's onion address exists only once its onion role has started,
	// after the ceremony, so `orama global tor onions add` puts them here.
	ValidatorOnions []string `json:"validator_onions,omitempty"`
}

// ParseNetwork reads a network file. Unknown fields are an error: a misspelt
// key must not silently leave a default in place.
func ParseNetwork(data []byte) (Network, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var n Network
	if err := dec.Decode(&n); err != nil {
		return Network{}, fmt.Errorf("parse the Tor network file: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Network{}, errors.New("parse the Tor network file: trailing data after the network object")
	}
	n.normalize()
	if err := n.Validate(); err != nil {
		return Network{}, err
	}
	return n, nil
}

// Marshal is the network file: indented JSON with a trailing newline.
func (n Network) Marshal() ([]byte, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode the Tor network file: %w", err)
	}
	return append(out, '\n'), nil
}

// Load reads and validates the network file at path.
func Load(path string) (Network, error) {
	f, err := os.Open(path)
	if err != nil {
		return Network{}, fmt.Errorf("open the Tor network file: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, NetworkFileLimit+1))
	if err != nil {
		return Network{}, fmt.Errorf("read the Tor network file: %w", err)
	}
	if len(raw) > NetworkFileLimit {
		return Network{}, fmt.Errorf("the Tor network file %s is larger than %d bytes", path, NetworkFileLimit)
	}
	n, err := ParseNetwork(raw)
	if err != nil {
		return Network{}, fmt.Errorf("%s: %w", path, err)
	}
	return n, nil
}

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

// WithValidatorOnions returns the network with onions added to the validator
// onion services it lists, in the canonical lower-case form. An onion that is
// already listed is not listed twice; the result is validated.
func (n Network) WithValidatorOnions(onions ...string) (Network, error) {
	out := n
	out.ValidatorOnions = append([]string(nil), n.ValidatorOnions...)
	listed := map[string]bool{}
	for _, o := range n.ValidatorOnions {
		listed[strings.ToLower(o)] = true
	}
	for _, o := range onions {
		if o = strings.ToLower(o); !listed[o] {
			listed[o] = true
			out.ValidatorOnions = append(out.ValidatorOnions, o)
		}
	}
	if err := out.Validate(); err != nil {
		return Network{}, err
	}
	return out, nil
}

func (n *Network) normalize() {
	for i := range n.ValidatorOnions {
		n.ValidatorOnions[i] = strings.ToLower(n.ValidatorOnions[i])
	}
	for i := range n.Authorities {
		a := &n.Authorities[i]
		a.V3Ident = strings.ToUpper(a.V3Ident)
		a.Fingerprint = strings.ToUpper(a.Fingerprint)
	}
}

// Validate checks the network is one authorities can run. It refuses what Tor
// would refuse at startup (an interval that does not divide a day, delays that
// do not fit in it) so a bad file fails at install, not on a restart.
func (n Network) Validate() error {
	if !n.Private {
		return ErrPublicNetwork
	}
	if !networkNamePattern.MatchString(n.Name) {
		return fmt.Errorf("tor network name %q must be 2-%d lowercase letters, digits and dashes", n.Name, maxNameLen)
	}
	if len(n.Authorities) < MinAuthorities {
		return fmt.Errorf("tor network %s needs at least %d authorities, has %d", n.Name, MinAuthorities, len(n.Authorities))
	}
	if err := n.validateSchedule(); err != nil {
		return err
	}
	if n.HSDirMinUptimeHours < 0 || n.HSDirMinUptimeHours > maxHSDirUptimeHours {
		return fmt.Errorf("hsdir_min_uptime_hours %d must be 0 (Tor's default) to %d", n.HSDirMinUptimeHours, maxHSDirUptimeHours)
	}
	if err := n.validateOnions(); err != nil {
		return err
	}
	seen := map[string]string{}
	for _, a := range n.Authorities {
		if err := a.validate(); err != nil {
			return fmt.Errorf("authority %q: %w", a.Nickname, err)
		}
		for _, f := range []struct{ what, value string }{
			{"nickname", a.Nickname}, {"address", a.Address}, {"fingerprint", a.Fingerprint},
			{"v3 identity", a.V3Ident}, {"ed25519 identity", a.Ed25519ID},
		} {
			key := f.what + "|" + f.value
			if other, dup := seen[key]; dup {
				return fmt.Errorf("authorities %q and %q share the %s %s", other, a.Nickname, f.what, f.value)
			}
			seen[key] = a.Nickname
		}
	}
	return nil
}

func (n Network) validateOnions() error {
	if len(n.ValidatorOnions) > maxValidatorOnions {
		return fmt.Errorf("a network lists at most %d validator onion services", maxValidatorOnions)
	}
	seen := map[string]bool{}
	for i, o := range n.ValidatorOnions {
		if _, err := chainonion.Base(o); err != nil {
			return fmt.Errorf("validator onion %d: %w", i, err)
		}
		if o != strings.ToLower(o) {
			return fmt.Errorf("validator onion %d %q must be lower case", i, o)
		}
		if seen[o] {
			return fmt.Errorf("validator onion %d %s is listed twice", i, o)
		}
		seen[o] = true
	}
	return nil
}

// HSDirIntervalMinutes is the length, in minutes, of the time period an onion
// service publishes a descriptor for: one shared-random protocol run, which is
// 24 voting intervals. The authorities write it into the consensus as the
// hsdir_interval parameter (torrc ConsensusParams), so every service and client
// of the network reads the same value from the consensus and needs no setting
// of its own.
//
// Tor's default is 1440 minutes, which is a protocol run only when the voting
// interval is the public network's 60 minutes. With a shorter interval a run
// ends several times inside one default period, and a service rotates its
// descriptors at the end of every run (rotate_all_descriptors in hs_service.c):
// each rotation promotes the descriptor for the NEXT period to current and
// closes the introduction circuits of the one clients still ask for, so the
// service is unreachable until the period itself ends. A period one run long,
// started half a run after the run does (the rotation offset Tor derives from
// the voting interval), is the arrangement of the public network at any interval.
func (n Network) HSDirIntervalMinutes() int { return sharedRandomRounds * n.VotingIntervalMinutes }

func (n Network) validateSchedule() error {
	if n.VotingIntervalMinutes < minVotingIntervalMinutes || minutesPerDay%n.VotingIntervalMinutes != 0 {
		return fmt.Errorf("voting_interval_minutes %d must be at least %d and divide 24 hours evenly", n.VotingIntervalMinutes, minVotingIntervalMinutes)
	}
	if n.HSDirIntervalMinutes() > maxHSDirIntervalMinutes {
		return fmt.Errorf("voting_interval_minutes %d makes a shared-random run of %d minutes, longer than the %d minutes Tor allows an onion service time period (hsdir_interval); use at most %d",
			n.VotingIntervalMinutes, n.HSDirIntervalMinutes(), maxHSDirIntervalMinutes, maxHSDirIntervalMinutes/sharedRandomRounds)
	}
	if n.VoteDelaySeconds < minDelaySeconds || n.DistDelaySeconds < minDelaySeconds {
		return fmt.Errorf("vote_delay_seconds %d and dist_delay_seconds %d must each be at least %d", n.VoteDelaySeconds, n.DistDelaySeconds, minDelaySeconds)
	}
	if (n.VoteDelaySeconds+n.DistDelaySeconds)*2 >= n.VotingIntervalMinutes*60 {
		return fmt.Errorf("vote_delay_seconds plus dist_delay_seconds must be less than half the voting interval (%d s)", n.VotingIntervalMinutes*60)
	}
	return nil
}

func (a Authority) validate() error {
	if !nicknamePattern.MatchString(a.Nickname) {
		return fmt.Errorf("nickname must be 1-%d letters and digits", maxNicknameLen)
	}
	if err := PublicIPv4(a.Address); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		port int
	}{{"or_port", a.ORPort}, {"dir_port", a.DirPort}} {
		if f.port < 1 || f.port > maxPort {
			return fmt.Errorf("%s %d is not a TCP port", f.name, f.port)
		}
	}
	if a.ORPort == a.DirPort {
		return fmt.Errorf("or_port and dir_port are both %d", a.ORPort)
	}
	if err := checkFingerprint("v3_ident", a.V3Ident); err != nil {
		return err
	}
	if err := checkFingerprint("fingerprint", a.Fingerprint); err != nil {
		return err
	}
	return checkEd25519ID(a.Ed25519ID)
}

func checkFingerprint(name, v string) error {
	if len(v) != fingerprintLen || v != strings.ToUpper(v) {
		return fmt.Errorf("%s must be %d uppercase hex digits", name, fingerprintLen)
	}
	if _, err := hex.DecodeString(v); err != nil {
		return fmt.Errorf("%s is not hex: %w", name, err)
	}
	return nil
}

func checkEd25519ID(v string) error {
	if len(v) != ed25519IDLen {
		return fmt.Errorf("ed25519_id must be %d unpadded base64 characters", ed25519IDLen)
	}
	raw, err := base64.RawStdEncoding.DecodeString(v)
	if err != nil || len(raw) != 32 {
		return errors.New("ed25519_id is not the unpadded base64 of a 32-byte key")
	}
	return nil
}

// PublicIPv4 refuses anything that is not a routable IPv4 address: a relay
// that publishes a private address is unreachable, and a directory authority
// with one would be listed by nobody.
func PublicIPv4(addr string) error {
	ip, err := netip.ParseAddr(addr)
	if err != nil || !ip.Is4() {
		return fmt.Errorf("%q is not an IPv4 address", addr)
	}
	if netguard.Reserved(net.IP(ip.AsSlice())) {
		return fmt.Errorf("%s is not a public address", addr)
	}
	return nil
}

// DirAuthorityLines are the torrc lines that replace Tor's built-in authority
// list with this network's. The address on each line is the DirPort, the
// fingerprint is the authority's relay identity, and v3ident is what its
// certificates are signed by.
func (n Network) DirAuthorityLines() []string {
	lines := make([]string, 0, len(n.Authorities))
	for _, a := range n.Authorities {
		lines = append(lines, fmt.Sprintf("DirAuthority %s orport=%d v3ident=%s %s %s",
			a.Nickname, a.ORPort, a.V3Ident, net.JoinHostPort(a.Address, fmt.Sprint(a.DirPort)), a.Fingerprint))
	}
	return lines
}

// AuthorityAt is the authority published at address, and whether there is one.
func (n Network) AuthorityAt(address string) (Authority, bool) {
	for _, a := range n.Authorities {
		if a.Address == address {
			return a, true
		}
	}
	return Authority{}, false
}
