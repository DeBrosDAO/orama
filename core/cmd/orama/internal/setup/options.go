// Package setup is `orama setup`: the one command that turns fresh VPSes into
// nodes of an Orama network.
//
// For every IP it enrolls the machine (a RootWallet SSH key and a pinned host
// key), checks the hardware against the profile, puts the signed release of the
// network's channel on it, installs the cluster node, installs the global layer
// beside it (the chain joins by state sync), and registers the operator, the
// nodes, their bonds and the validator on the chain, signing through the
// RootWallet. Re-running it with more IPs adds nodes to the same cluster, and a
// machine that already has a step is not given it again.
//
// plan.go decides what each IP gets and is pure. run.go is the order the steps
// run in; everything it does to a machine, a network, a repository or the chain
// goes through the ports in ports.go, so the order and the decisions are tested
// without a machine. wizard/ is the terminal UI over the same plan.
package setup

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/nodenames"
)

const (
	// DefaultStorageGB is the public storage a full node offers when --storage-gb
	// is not given: it counts towards the disk floor and sizes the public Kubo
	// and the capacity the node declares.
	DefaultStorageGB uint64 = 50
	// DefaultSSHUser is the login setup uses when --user is not given.
	DefaultSSHUser = "root"
	// MaxStorageGB bounds --storage-gb: a petabyte is a typo, and the byte counts
	// derived from it must not overflow.
	MaxStorageGB uint64 = 1_000_000
	// MaxNodes bounds one run: a join mints an invite per node and the chain
	// takes an operator's transactions one at a time.
	MaxNodes = 20
)

// nameRE is a node name: it is the node's id on the chain and its moniker, and
// the label of the name the chain can claim for it. Lowercase letters, digits
// and single hyphens, 2 to 32 characters.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

const (
	nodeNameMin = 2
	nodeNameMax = 32
)

var (
	// sshUserRE is a POSIX login name: it goes into an ssh argument, where a
	// leading '-' would be an option.
	sshUserRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	// domainRE is a DNS name of at least two lowercase labels.
	domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// envRE is the name of a CLI environment.
	envRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// Options is everything the operator decided: the flags, or the answers of the
// wizard.
type Options struct {
	// Network is a registry network name or a manifest URL; empty means the
	// active network, or the only one the registry has.
	Network string
	// IPs are the machines, in the order they are set up. The first creates the
	// cluster unless the environment already has one.
	IPs []string
	// Name is the node name; for several IPs the nodes are Name, Name-2, ....
	Name string
	// ClusterOnly installs the cluster node and none of the global layer.
	ClusterOnly bool
	// Exit makes the relay an exit relay. Opt-in, and the operator carries the
	// abuse complaints about whatever leaves it.
	Exit bool
	// ExitConfirmed says the operator saw the exit warning and accepted it (Yes
	// also does).
	ExitConfirmed bool
	// StorageGB is the public storage each full node offers. Zero means
	// DefaultStorageGB.
	StorageGB uint64
	// Yes answers every question with its default and runs unattended.
	Yes bool

	// User is the SSH login; UsePassword and BootstrapKey say how the first
	// connection is made (see production/setup.EnrollRequest). Password is typed
	// into the wizard for this run and never stored.
	User string
	// Users are the logins given for single machines as --ip <user>@<address>; a machine without
	// one uses User. Providers differ (one image logs in as root, another as ubuntu), and the
	// machines of one network are often rented from several.
	Users        map[string]string
	UsePassword  bool
	Password     string
	BootstrapKey string
	// HostKeys are the expected SSH host-key fingerprints by IP; the key "" is
	// the one fingerprint of a single-IP run.
	HostKeys map[string]string

	// Domain is the base domain of a private cluster on the operator's own
	// domain; setup prints the NS and glue records it needs and waits for them.
	Domain string
	ACMECA string
	// Env is the CLI environment the cluster is recorded under. Empty reuses the
	// active one when it runs on this network, else <network>-<name>.
	Env string
	// Contact is where an abuse complaint about the relay goes; required with
	// Exit, and the operator's account otherwise.
	Contact string
	// ASN overrides the autonomous system number looked up for each IP; 0 with
	// ASNSet leaves it undeclared.
	ASN    uint32
	ASNSet bool
	// TorNetwork is the path of a tor-network.json to give the relays instead of the
	// one the network's manifest pins. A network that pins none installs a relay
	// only with it.
	TorNetwork string
	// NoRelay leaves the relay out though the network pins a Tor network.
	NoRelay bool
	// UploadRelease makes this computer download the release once and upload it to
	// each machine over SSH, instead of each machine downloading it from the
	// release repository: for machines that cannot reach the repository. Without
	// it every machine downloads the release itself, and one that cannot fails the
	// run; setup never switches to the upload by itself.
	UploadRelease bool
	// AllowQuorumLoss lets the run restart a cluster node with --force when the cluster
	// has fewer than three voters, so the restart takes its quorum down for as long as
	// the node is away. Without it (or a yes to the plan that shows it) that restart
	// stops the run.
	AllowQuorumLoss bool
	// NoValidator skips creating the validator (and the 1,000 ORAMA self-bond).
	NoValidator bool
	// Create, when set, makes the run create the network instead of joining one:
	// the machines are its bootstrap committee.
	Create *CreateOptions
}

// Normalize checks the options and fills the defaults. It touches no machine.
func (o *Options) Normalize() error {
	o.Name = strings.TrimSpace(strings.ToLower(o.Name))
	if o.User == "" {
		o.User = DefaultSSHUser
	}
	ips, users, err := splitLogins(o.IPs)
	if err != nil {
		return err
	}
	if ips, err = normalizeIPs(ips); err != nil {
		return err
	}
	o.IPs = ips
	for ip, user := range users {
		if o.Users == nil {
			o.Users = map[string]string{}
		}
		o.Users[net.ParseIP(ip).To4().String()] = user
	}
	if !sshUserRE.MatchString(o.User) {
		return clierr.Usage("--user %q is not a login name", o.User)
	}
	if o.Env != "" && !envRE.MatchString(o.Env) {
		return clierr.Usage("--env %q: use lowercase letters, digits, '.', '_' and '-'", o.Env)
	}
	if o.UsePassword && o.BootstrapKey != "" {
		return clierr.Usage("--password and --bootstrap-key are alternatives; pass one")
	}
	if err := o.checkHostKeys(); err != nil {
		return err
	}
	if o.Domain != "" {
		o.Domain = strings.ToLower(strings.TrimSuffix(o.Domain, "."))
		if !domainRE.MatchString(o.Domain) {
			return clierr.Usage("--domain %q is not a DNS name such as cluster.example.org", o.Domain)
		}
	}
	if o.Create != nil {
		if err := o.prepareCreate(); err != nil {
			return err
		}
	}
	if err := o.checkProfile(); err != nil {
		return err
	}
	return nil
}

// UserFor is the SSH login of the machine at ip: its own from --ip <user>@<address>, else --user.
func (o *Options) UserFor(ip string) string {
	if user, ok := o.Users[ip]; ok {
		return user
	}
	return o.User
}

// splitLogins takes the <user>@ off each --ip that carries one and returns the bare addresses and
// the logins by address. A login that is not a login name is refused.
func splitLogins(in []string) ([]string, map[string]string, error) {
	out := make([]string, 0, len(in))
	users := map[string]string{}
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		user, ip, found := strings.Cut(raw, "@")
		if !found {
			out = append(out, raw)
			continue
		}
		if !sshUserRE.MatchString(user) {
			return nil, nil, clierr.Usage("--ip %q: %q is not a login name", raw, user)
		}
		users[ip] = user
		out = append(out, ip)
	}
	return out, users, nil
}

// ParseIPList splits a pasted list of addresses (spaces, commas, semicolons or
// lines), each an IPv4 address or <user>@<address>, and checks each address is a public
// IPv4 address. The entries keep their login, for Normalize.
func ParseIPList(list string) ([]string, error) {
	fields := strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
	ips, users, err := splitLogins(fields)
	if err != nil {
		return nil, err
	}
	if ips, err = normalizeIPs(ips); err != nil {
		return nil, err
	}
	for i, ip := range ips {
		if user := users[ip]; user != "" {
			ips[i] = user + "@" + ip
		}
	}
	return ips, nil
}

func normalizeIPs(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, clierr.Usage("name the machines to set up: --ip <address> (repeatable) or the addresses as arguments")
	}
	if len(in) > MaxNodes {
		return nil, clierr.Usage("%d machines in one run; the most is %d", len(in), MaxNodes)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		ip := strings.TrimSpace(raw)
		if err := install.ValidatePublicIP(ip); err != nil {
			return nil, clierr.Usage("--ip: %v", err)
		}
		ip = net.ParseIP(ip).To4().String()
		if seen[ip] {
			return nil, clierr.Usage("--ip %s is listed twice", ip)
		}
		seen[ip] = true
		out = append(out, ip)
	}
	return out, nil
}

func (o *Options) checkHostKeys() error {
	if fp, ok := o.HostKeys[""]; ok && len(o.IPs) > 1 {
		return clierr.Usage("a bare --host-key %s names one machine; with several IPs pass --host-key <ip>=<fingerprint> for each", fp)
	}
	for ip := range o.HostKeys {
		if ip == "" {
			continue
		}
		if !contains(o.IPs, ip) {
			return clierr.Usage("--host-key names %s, which is not one of the machines (%s)", ip, strings.Join(o.IPs, ", "))
		}
	}
	return nil
}

func (o *Options) checkProfile() error {
	if o.ClusterOnly {
		switch {
		case o.Exit:
			return clierr.Usage("--exit is a relay of the global layer; it cannot go with --cluster-only")
		case o.StorageGB != 0:
			return clierr.Usage("--storage-gb is the public storage of the global layer; it cannot go with --cluster-only")
		case o.TorNetwork != "":
			return clierr.Usage("--tor-network is for the relay of the global layer; it cannot go with --cluster-only")
		case o.NoRelay:
			return clierr.Usage("--no-relay is for the global layer; --cluster-only has no relay")
		}
	} else {
		if o.StorageGB == 0 {
			o.StorageGB = DefaultStorageGB
		}
		if o.StorageGB > MaxStorageGB {
			return clierr.Usage("--storage-gb %d is more than the %d GB setup accepts", o.StorageGB, MaxStorageGB)
		}
		if err := ValidateFullNodeName(o.Name); err != nil {
			return clierr.Usage("--name: %v (a full node's name is its id on the chain and the name it claims; --cluster-only needs none)", err)
		}
	}
	if o.Name != "" && o.ClusterOnly {
		if err := ValidateNodeName(o.Name); err != nil {
			return clierr.Usage("--name: %v", err)
		}
	}
	if o.Exit && !o.Yes && !o.ExitConfirmed {
		return clierr.Usage("--exit makes this machine an exit relay.\n%s\nPass --yes to accept this, or leave --exit out", ExitWarning)
	}
	switch {
	case o.NoRelay && o.TorNetwork != "":
		return clierr.Usage("--no-relay and --tor-network are alternatives")
	case o.NoRelay && o.Exit:
		return clierr.Usage("--exit is a relay; it cannot go with --no-relay")
	}
	return nil
}

// ExitWarning is what an operator accepts by running an exit relay.
const ExitWarning = "An exit relay sends other people's traffic to the internet from your IP address. " +
	"Whatever they do there is traced to you: abuse complaints go to your hosting provider and to the " +
	"contact you give, and some providers forbid it. Check your provider's terms first."

// ValidateNodeName checks a node name.
func ValidateNodeName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a name is required")
	case len(name) < nodeNameMin || len(name) > nodeNameMax:
		return fmt.Errorf("%q must be %d to %d characters", name, nodeNameMin, nodeNameMax)
	case !nameRE.MatchString(name):
		return fmt.Errorf("%q must be lowercase letters, digits and single hyphens, starting with a letter", name)
	}
	return nil
}

// ValidateFullNodeName checks the name of a full node, which is also the name it
// claims on the chain: ValidateNodeName, and the chain's grammar and reserved words.
func ValidateFullNodeName(name string) error {
	if err := ValidateNodeName(name); err != nil {
		return err
	}
	if err := nodenames.ValidateName(name); err != nil {
		return fmt.Errorf("%q %w", name, err)
	}
	return nil
}

// NodeNames are the names of n nodes under one base: base, base-2, base-3, ...
func NodeNames(base string, n int) []string {
	names := make([]string, n)
	for i := range names {
		if i == 0 {
			names[i] = base
		} else {
			names[i] = fmt.Sprintf("%s-%d", base, i+1)
		}
	}
	return names
}

func contains(list []string, s string) bool { return slices.Contains(list, s) }
