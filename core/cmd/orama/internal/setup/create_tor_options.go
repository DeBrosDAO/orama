package setup

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// DefaultTorAuthorities is how many directory authorities a created network's own Tor
	// network gets when --tor-authorities is not given: the smallest set that can lose one
	// and keep a majority (tornet.MinAuthorities).
	DefaultTorAuthorities = tornet.MinAuthorities

	// torVotingIntervalMinutes is the new network's voting interval. A consensus can come
	// no sooner than the authorities' 30 minutes of learning (tornet.AuthorityLearnWindow),
	// so a shorter interval buys nothing and a longer one only waits longer for it. It
	// divides 24 hours, and 24 of them (the shared-random run) are the 720 minutes the
	// authorities vote as hsdir_interval.
	torVotingIntervalMinutes = 30
	// torDelaySeconds is the authorities' vote and distribution delay, the ceremony's default.
	torDelaySeconds = 300
	// torHSDirMinUptimeHours lets a relay hold the HSDir flag an hour after it starts: a new
	// network cannot wait Tor's 96 hours for the validators' onion services to publish.
	torHSDirMinUptimeHours = 1
	// torAuthorityNicknameFormat names the nth authority; a relay's nickname is
	// tornet.NicknameFor(its node name), 14 hex digits after "Orama".
	torAuthorityNicknameFormat = "OramaAuth%02d"
	// minDistinctSubnets is how many /16 networks a circuit needs: Tor avoids two relays of
	// one /16 in a circuit, and a network with fewer cannot build one.
	minDistinctSubnets = 3
	// torMinCertValidity is the least a new authority's signing certificate must have left: it
	// is made for 12 months, and one that expires within a month is not one a network starts with.
	torMinCertValidity = 30 * 24 * time.Hour
)

// prepareTor settles how many machines run a directory authority and refuses what
// cannot work, before any machine is touched.
func (o *Options) prepareTor() error {
	c := o.Create
	machines := len(o.IPs)
	if !c.TorAuthoritiesSet {
		c.TorAuthorities = 0
		if machines >= DefaultTorAuthorities && o.TorNetwork == "" && !o.NoRelay {
			c.TorAuthorities = DefaultTorAuthorities
		}
	}
	n := c.TorAuthorities
	switch {
	case n == 0:
		return nil
	case n < 0:
		return clierr.Usage("--tor-authorities %d: give 0 to run no Tor network, or at least %d", n, tornet.MinAuthorities)
	case o.TorNetwork != "":
		return clierr.Usage("--tor-authorities makes this network's own Tor network and --tor-network gives the relays another one: pass one of them")
	case o.NoRelay:
		return clierr.Usage("--tor-authorities makes the nodes run Tor roles and --no-relay says they run none: pass one of them, or --tor-authorities 0")
	case n < tornet.MinAuthorities:
		return clierr.Usage("--tor-authorities %d: a Tor network needs at least %d directory authorities, because fewer cannot lose one and still hold a majority; pass 0 to run no Tor network", n, tornet.MinAuthorities)
	case n > machines:
		return clierr.Usage("--tor-authorities %d needs %d machines, one for each authority, and this run has %d", n, n, machines)
	case o.Exit && n == machines && !c.TorAuthoritiesSet:
		return clierr.Usage("with %d machines all are directory authorities by default, and an authority is never an exit: --exit needs a machine that is not one. "+
			"Pass --tor-authorities 0 (and --tor-network, for the relays' Tor network), or add a machine", machines)
	case o.Exit && n == machines:
		return clierr.Usage("--exit makes a relay an exit, and --tor-authorities %d makes all %d machines authorities (an authority is never an exit, and a machine is a relay or an authority, not both): add machines for the relays, give the relays another network with --tor-network, or leave --exit out", n, machines)
	case !tornet.ValidNetworkName(c.Name):
		return clierr.Usage("the network name %q cannot name a Tor network (2 to 48 lowercase letters, digits and dashes): pass --tor-authorities 0, or another name", c.Name)
	}
	for _, ip := range o.IPs {
		if err := tornet.PublicIPv4(ip); err != nil {
			return clierr.Usage("the machine %s cannot be in a Tor network: %v (pass --tor-authorities 0 to run no Tor network)", ip, err)
		}
	}
	return nil
}

// ownTorNetwork says the run creates the network's own Tor network.
func ownTorNetwork(o Options) bool { return o.Create != nil && o.Create.TorAuthorities > 0 }

// torRoleOf is the role of the machine at position i in the plan: the first
// TorAuthorities machines run an authority, the others a relay.
func torRoleOf(c *CreateOptions, i int) TorRole {
	if i < c.TorAuthorities {
		return TorDirauth
	}
	return TorRelay
}

// applyTor gives each node of a creation its Tor role and, for a relay, the on-chain
// roles of a relay. The services of the chain layer stay as they are: the Tor roles
// are installed after the chain runs.
func (p *Plan) applyTor(o Options) {
	c := o.Create
	for i := range p.Nodes {
		n := &p.Nodes[i]
		if !n.Full() {
			continue
		}
		n.Tor = torRoleOf(c, i)
		n.Exit = o.Exit && n.Tor == TorRelay
		if n.Tor == TorRelay {
			n.Roles = append(n.Roles, torRelayRoles(o.Exit)...)
		}
	}
	p.Notes = append(p.Notes, torNote(p, c.TorAuthorities, o.Exit))
}

// torNote tells the operator what the Tor step does and how long it takes.
func torNote(p *Plan, authorities int, exit bool) string {
	relays := len(p.Nodes) - authorities
	what := fmt.Sprintf("%d machines run directory authorities of the network's own Tor network, made on those machines (each identity key stays on its machine, encrypted, with its passphrase beside it on the same machine: a test network's arrangement, which a production Tor network does not use)", authorities)
	if relays > 0 {
		kind := "relays"
		if exit {
			kind = "EXIT relays"
		}
		what += fmt.Sprintf(", and the other %d run %s", relays, kind)
	}
	return what + fmt.Sprintf(". The authorities cast no Running vote for %s after they start, so the first consensus comes about "+
		"an hour in, and leaving the bootstrap mode restarts them one at a time, %s apart: expect this step to take two hours or more. "+
		"Its tor-network.json is pinned in the manifest", tornet.AuthorityLearnWindow, tornet.AuthorityLearnWindow)
}

// distinctSubnets is how many /16 networks the addresses are in.
func distinctSubnets(ips []string) int {
	seen := map[string]bool{}
	for _, ip := range ips {
		if v4 := net.ParseIP(ip).To4(); v4 != nil {
			seen[fmt.Sprintf("%d.%d", v4[0], v4[1])] = true
		}
	}
	return len(seen)
}

// torAuthorityNickname is the nickname of the authority on the ith machine.
func torAuthorityNickname(i int) string { return fmt.Sprintf(torAuthorityNicknameFormat, i+1) }

// torSeatLines are the authorities as the operator reads them against the machines' own output.
func torSeatLines(auths []tornet.Authority) string {
	lines := make([]string, len(auths))
	for i, a := range auths {
		lines[i] = fmt.Sprintf("%s %s fingerprint %s v3ident %s", a.Nickname, a.Address, a.Fingerprint, a.V3Ident)
	}
	return strings.Join(lines, "\n")
}
