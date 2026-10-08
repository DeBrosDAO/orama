package tornet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// nicknamePrefix and nicknameDigits make NicknameFor's 19 characters.
	nicknamePrefix = "Orama"
	nicknameDigits = 14

	// onionPort is the port the validator onion service answers on. The
	// transaction clients (chainonion) dial port 80 of an onion address.
	onionPort = 80
	// onionMaxStreams caps the streams one rendezvous circuit may carry; a
	// circuit past it is closed.
	onionMaxStreams = 20

	// clientSOCKSHost is the loopback listener of a client torrc.
	clientSOCKSHost = "127.0.0.1"
)

// RelayConfig is a relay, an exit, or (with Authority set) a directory
// authority: one tor process that serves the network its ORPort.
type RelayConfig struct {
	Network Network
	// Home is the DataDirectory.
	Home string
	// Nickname is NicknameFor(node id) for a relay, the authority's own for a
	// directory authority.
	Nickname string
	// Contact is published in the descriptor so the people who run the network
	// and the people who receive complaints can reach the operator.
	Contact string
	// Address is the public IPv4 the relay publishes. A relay behind the
	// co-located namespace's NAT cannot discover it.
	Address string
	ORPort  int
	DirPort int
	// Exit makes the relay an exit under ExitPolicyLines. It is refused unless
	// the network allows exits.
	Exit bool
	// ExitReject is the operator's list of refused destinations (ParseExitRejectList).
	ExitReject []string
	// Family are the RSA fingerprints of the other relays of this operator.
	Family []string
	// BandwidthMbit limits the traffic this relay carries for others, in each
	// direction. Zero leaves it unlimited.
	BandwidthMbit uint
	// Authority makes the process a v3 directory authority. A directory
	// authority is a relay that votes; it never exits.
	Authority bool
	// BandwidthFile is a bandwidth scanner's output the authority votes with.
	BandwidthFile string
}

// NicknameFor is the relay nickname derived from an on-chain node id: stable,
// 19 letters and digits (Tor's limit), and not the id itself, so a public
// descriptor does not name the registration.
func NicknameFor(nodeID string) string {
	sum := sha256.Sum256([]byte(nodeID))
	return nicknamePrefix + hex.EncodeToString(sum[:])[:nicknameDigits]
}

// RelayTorrc renders the torrc of a relay, exit or directory authority.
func RelayTorrc(c RelayConfig) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	var b torrc
	b.comment("Managed by Orama Network: rewritten on every `orama global install`.")
	b.comment("Relay of the Orama Tor network " + c.Network.Name + " (docs/TOR_NETWORK.md).")
	b.line("DataDirectory " + c.Home)
	b.line("Log notice stdout")
	b.line("SafeLogging 1")
	b.network(c.Network)
	b.line("SocksPort 0")
	b.line("Nickname " + c.Nickname)
	b.line("ContactInfo " + c.Contact)
	b.line("Address " + c.Address)
	b.line(fmt.Sprintf("ORPort %d IPv4Only", c.ORPort))
	if c.Authority {
		b.line(fmt.Sprintf("DirPort %d", c.DirPort))
	} else {
		b.line("DirPort 0")
	}
	if c.Network.Bootstrap {
		b.comment("Bootstrap: the network has no consensus yet. Remove `bootstrap` from the network file once it has one.")
		b.line("AssumeReachable 1")
	}
	if c.BandwidthMbit > 0 {
		b.line(fmt.Sprintf("RelayBandwidthRate %d Mbits", c.BandwidthMbit))
		b.line(fmt.Sprintf("RelayBandwidthBurst %d Mbits", c.BandwidthMbit))
	}
	if len(c.Family) > 0 {
		b.line("MyFamily " + familyList(c.Family))
	}
	if c.Authority {
		b.authority(c)
	}
	b.exit(c)
	return b.String(), nil
}

func (b *torrc) authority(c RelayConfig) {
	n := c.Network
	b.line("AuthoritativeDirectory 1")
	b.line("V3AuthoritativeDirectory 1")
	b.line(fmt.Sprintf("V3AuthVotingInterval %d minutes", n.VotingIntervalMinutes))
	b.line(fmt.Sprintf("V3AuthVoteDelay %d seconds", n.VoteDelaySeconds))
	b.line(fmt.Sprintf("V3AuthDistDelay %d seconds", n.DistDelaySeconds))
	b.comment("Sybil control: at most one relay per address is listed.")
	b.line("AuthDirMaxServersPerAddr 1")
	if n.HSDirMinUptimeHours > 0 {
		b.comment("A new network cannot wait the default 96 hours for onion services to have directories.")
		b.line(fmt.Sprintf("MinUptimeHidServDirectoryV2 %d hours", n.HSDirMinUptimeHours))
	}
	if c.BandwidthFile != "" {
		b.line("V3BandwidthsFile " + c.BandwidthFile)
	}
}

func (b *torrc) exit(c RelayConfig) {
	if !c.Exit {
		b.line("ExitRelay 0")
		b.line("ExitPolicy reject *:*")
		return
	}
	b.comment("Exit: opt-in; the policy refuses mail, file sharing and every reserved range.")
	b.line("ExitRelay 1")
	b.line("IPv6Exit 0")
	for _, l := range ExitPolicyLines(c.ExitReject) {
		b.line(l)
	}
}

func (c RelayConfig) validate() error {
	if err := c.Network.Validate(); err != nil {
		return err
	}
	if err := checkHome(c.Home); err != nil {
		return err
	}
	if !nicknamePattern.MatchString(c.Nickname) {
		return fmt.Errorf("relay nickname %q must be 1-%d letters and digits", c.Nickname, maxNicknameLen)
	}
	if err := checkContact(c.Contact); err != nil {
		return err
	}
	if err := PublicIPv4(c.Address); err != nil {
		return fmt.Errorf("relay address: %w", err)
	}
	if c.ORPort < 1 || c.ORPort > maxPort || (c.Authority && (c.DirPort < 1 || c.DirPort > maxPort || c.DirPort == c.ORPort)) {
		return fmt.Errorf("relay ports ORPort %d DirPort %d are not valid TCP ports", c.ORPort, c.DirPort)
	}
	if c.Authority && c.Exit {
		return errors.New("a directory authority does not exit")
	}
	if c.Exit && !c.Network.AllowExit {
		return fmt.Errorf("network %s does not allow exits: its network file has allow_exit false", c.Network.Name)
	}
	if !c.Exit && len(c.ExitReject) > 0 {
		return errors.New("an exit reject list needs the exit role")
	}
	for _, r := range c.ExitReject {
		if _, err := exitRejectRule(r); err != nil {
			return fmt.Errorf("exit reject rule %q: %w", r, err)
		}
	}
	for _, f := range c.Family {
		if err := checkFingerprint("family member", f); err != nil {
			return err
		}
	}
	if c.BandwidthFile != "" {
		if err := checkHome(c.BandwidthFile); err != nil {
			return fmt.Errorf("bandwidth file: %w", err)
		}
	}
	return nil
}

func familyList(fps []string) string {
	out := make([]string, len(fps))
	for i, f := range fps {
		out[i] = "$" + f
	}
	return strings.Join(out, ",")
}

// OnionConfig is the tor process that publishes a validator's onion service.
type OnionConfig struct {
	Network Network
	Home    string
	// Target is the address of the tx gate the onion service forwards to.
	Target string
}

// OnionTorrc renders the torrc of the validator onion service: a client of the
// network with one hidden service and no ORPort, so it relays nothing. The
// service answers on port 80, where the transaction clients dial.
func OnionTorrc(c OnionConfig) (string, error) {
	if err := c.Network.Validate(); err != nil {
		return "", err
	}
	if err := checkHome(c.Home); err != nil {
		return "", err
	}
	if err := checkTarget(c.Target); err != nil {
		return "", err
	}
	var b torrc
	b.comment("Managed by Orama Network: rewritten on every `orama global install`.")
	b.comment("Validator onion service of the Orama Tor network " + c.Network.Name + " (docs/TOR_NETWORK.md).")
	b.line("DataDirectory " + c.Home)
	b.line("Log notice stdout")
	b.line("SafeLogging 1")
	b.network(c.Network)
	b.line("SocksPort 0")
	b.line("ORPort 0")
	b.line("DirPort 0")
	b.line("ClientOnly 1")
	b.line("HiddenServiceDir " + c.Home + "/onion")
	b.line("HiddenServiceVersion 3")
	b.line(fmt.Sprintf("HiddenServicePort %d %s", onionPort, c.Target))
	b.line("HiddenServiceEnableIntroDoSDefense 1")
	b.line(fmt.Sprintf("HiddenServiceMaxStreams %d", onionMaxStreams))
	b.line("HiddenServiceMaxStreamsCloseCircuit 1")
	return b.String(), nil
}

// ClientConfig is a client of the Orama network: a wallet or an app that
// builds circuits through it.
type ClientConfig struct {
	Network Network
	Home    string
	// SOCKSPort is the loopback port the client listens on.
	SOCKSPort int
}

// ClientTorrc renders the torrc of a client of the Orama network. The SOCKS
// listener is loopback only and isolates streams by SOCKS credentials, so a
// caller that sends a new credential per request gets a new circuit.
func ClientTorrc(c ClientConfig) (string, error) {
	if err := c.Network.Validate(); err != nil {
		return "", err
	}
	if err := checkHome(c.Home); err != nil {
		return "", err
	}
	if c.SOCKSPort < 1 || c.SOCKSPort > maxPort {
		return "", fmt.Errorf("SOCKS port %d is not a TCP port", c.SOCKSPort)
	}
	var b torrc
	b.comment("Client of the Orama Tor network " + c.Network.Name + " (docs/TOR_NETWORK.md).")
	b.line("DataDirectory " + c.Home)
	b.line("Log notice stdout")
	b.line("SafeLogging 1")
	b.network(c.Network)
	b.line(fmt.Sprintf("SocksPort %s:%d IsolateSOCKSAuth", clientSOCKSHost, c.SOCKSPort))
	b.line("ClientOnly 1")
	b.line("ORPort 0")
	b.line("DirPort 0")
	b.line("ExitRelay 0")
	b.line("ClientRejectInternalAddresses 1")
	return b.String(), nil
}

type torrc struct{ strings.Builder }

func (b *torrc) line(s string)    { b.WriteString(s + "\n") }
func (b *torrc) comment(s string) { b.WriteString("# " + s + "\n") }

// network is what every role has in common: the network's authorities in
// place of Tor's built-in ones and no fallback directories of the public network.
func (b *torrc) network(n Network) {
	b.line("UseDefaultFallbackDirs 0")
	for _, l := range n.DirAuthorityLines() {
		b.line(l)
	}
	if n.AllowSharedSubnets {
		b.comment("A small network: circuits may use two relays of one /16.")
		b.line("EnforceDistinctSubnets 0")
	}
}

// checkHome refuses a DataDirectory that is not a plain absolute path: it is
// written into a torrc line.
func checkHome(home string) error {
	if !strings.HasPrefix(home, "/") || strings.ContainsAny(home, " \t\r\n\"'\\#") || strings.Contains(home, "..") {
		return fmt.Errorf("tor DataDirectory %q must be a plain absolute path", home)
	}
	return nil
}

// checkTarget accepts host:port as a hidden service target.
func checkTarget(t string) error {
	host, port, ok := strings.Cut(t, ":")
	if !ok || host == "" || strings.ContainsAny(host, " \t\r\n:#") {
		return fmt.Errorf("onion target %q must be ipv4:port", t)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > maxPort || strconv.Itoa(p) != port {
		return fmt.Errorf("onion target %q: %q is not a TCP port", t, port)
	}
	return nil
}

// checkContact refuses a ContactInfo that could end the line it is written on.
func checkContact(c string) error {
	if c == "" || len(c) > maxContactLength {
		return fmt.Errorf("tor contact must be 1-%d characters", maxContactLength)
	}
	for _, r := range c {
		if r < 0x20 || r > 0x7e {
			return errors.New("tor contact must be printable ASCII on one line")
		}
	}
	if strings.ContainsAny(c, "#\\\"") {
		return errors.New("tor contact must not contain #, a backslash or a double quote")
	}
	return nil
}
