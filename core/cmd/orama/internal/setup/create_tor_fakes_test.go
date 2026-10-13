package setup

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// torIPs are public addresses (the documentation range the other tests use is refused by
// a Tor network file), in four /16 networks so no circuit needs shared subnets.
var torIPs = []string{"57.129.166.16", "57.129.166.17", "161.97.184.199", "161.97.184.202", "161.97.151.255"}

const (
	// simBoundary is the voting interval of the simulated network, and simLearn the
	// time an authority casts no Running vote after it starts.
	simBoundary = 30 * time.Minute
	simLearn    = 30 * time.Minute
	// simTick is how far each question put to a simulated machine moves its clock.
	simTick = time.Minute
)

// torSim simulates the Tor network the machines of a creation run, with a clock that
// moves when the machines are asked something: a consensus is made at every half
// hour at which a majority of the authorities have run for the learning window, a
// machine holds the latest one made while it ran (or before its restart), and an
// authority is refused a restart while another has started inside the window, as
// the real machine refuses it (globalnode.CheckAuthorityRoll).
type torSim struct {
	mu          sync.Mutex
	now         time.Time
	machines    map[string]*simTor
	order       []string
	authorities int
	// restarts logs, for each authority restart that went through, the time it did.
	restarts []simRestart
	// noGuard makes the machines accept any restart, as a forced one would.
	noGuard bool
	// failRestart, when set, fails every restart of that address with that error.
	failRestart map[string]error
	failInstall map[string]error
	failStart   map[string]error
	// neverListed makes the consensus leave out the machine at that address.
	neverListed map[string]bool
	// lag makes the machine fetch consensuses that long after they are made.
	lag map[string]time.Duration
	// notRunning makes the consensus list the machine without the Running flag.
	notRunning map[string]bool
}

type simRestart struct {
	ip string
	at time.Time
}

type simTor struct {
	idx            int
	role           TorRole
	installed      bool
	active         bool
	sha            string
	bootstrap      bool
	firstStart     time.Time
	startedAt      time.Time
	restartPending bool
	keys           *tornet.AuthorityKeys
}

func newTorSim() *torSim {
	return &torSim{now: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC), machines: map[string]*simTor{},
		failRestart: map[string]error{}, failInstall: map[string]error{}, failStart: map[string]error{}, neverListed: map[string]bool{}, notRunning: map[string]bool{}, lag: map[string]time.Duration{}}
}

// machine is the simulated state of the machine at ip.
func (s *torSim) machine(ip string) *simTor {
	m, ok := s.machines[ip]
	if !ok {
		m = &simTor{idx: len(s.order)}
		s.machines[ip] = m
		s.order = append(s.order, ip)
	}
	return m
}

func (s *torSim) tick() { s.now = s.now.Add(simTick) }

// latestConsensus is the latest boundary at which a consensus was made that the machine
// can hold, and the number of authorities that signed it. The authorities that
// signed it are counted at the boundary, but a machine started or restarted later
// still counts as running then only if it had been.
func (s *torSim) latestConsensus(m *simTor, at time.Time) (time.Time, int, bool) {
	for b := at.Truncate(simBoundary); b.After(at.Add(-4 * simBoundary)); b = b.Add(-simBoundary) {
		signed := s.runningAuthoritiesAtBoundary(b)
		if signed < s.authorities/2+1 {
			continue
		}
		if m.firstStart.IsZero() || b.Before(m.firstStart) {
			return time.Time{}, 0, false
		}
		return b, signed, true
	}
	return time.Time{}, 0, false
}

// runningAuthoritiesAtBoundary counts the authorities that were up at b and had run for
// the window: a restart after b does not take away the vote it cast at b, so the
// start times of restarts are not looked at, only the first start and the log of restarts.
func (s *torSim) runningAuthoritiesAtBoundary(b time.Time) int {
	n := 0
	for ip, m := range s.machines {
		if m.role != TorDirauth || m.firstStart.IsZero() {
			continue
		}
		lastStart := m.firstStart
		for _, r := range s.restarts {
			if r.ip == ip && !r.at.After(b) && r.at.After(lastStart) {
				lastStart = r.at
			}
		}
		if !lastStart.Add(simLearn).After(b) {
			n++
		}
	}
	return n
}

func (s *torSim) info(ip string, m *simTor) *tornet.NodeInfo {
	if m.firstStart.IsZero() {
		return nil
	}
	info := &tornet.NodeInfo{Home: torHomeOf(m.role), Nickname: fmt.Sprintf("Nick%d", m.idx), Fingerprint: fmt.Sprintf("%040X", 0xC0+m.idx)}
	if b, signed, ok := s.latestConsensus(m, s.now.Add(-s.lag[ip])); ok {
		flags := []string{"Fast", "Running", "Valid"}
		if s.notRunning[ip] {
			flags = []string{"Fast", "Valid"}
		}
		info.Consensus = &tornet.ConsensusInfo{ListedFlags: flags,
			Flavor: "ns", ValidAfter: b, FreshUntil: b.Add(simBoundary), ValidUntil: b.Add(3 * simBoundary),
			Fresh: s.now.Before(b.Add(simBoundary)), Valid: s.now.Before(b.Add(3 * simBoundary)), Signatures: signed,
			Listed: !s.neverListed[ip] && b.After(m.firstStart),
		}
	}
	return info
}

// exitCoded is a failed command's exit status as the SSH session reports it
// (*exec.ExitError has the same ExitCode method).
type exitCoded struct {
	code int
	msg  string
}

func (e exitCoded) Error() string { return fmt.Sprintf("exit status %d: %s", e.code, e.msg) }
func (e exitCoded) ExitCode() int { return e.code }

// notYetError is the refusal of `orama global restart dirauth`, as a machine's session returns it.
func notYetError(ip, msg string) error {
	return fmt.Errorf("restart the dirauth: run on %s: %w", ip, exitCoded{code: clierr.CodeNotYet, msg: msg})
}

// fakeAuthority is the identity the simulated machine at index idx makes for nickname.
func fakeAuthority(idx int, in AuthorityKeysInput) tornet.Authority {
	return tornet.Authority{
		Nickname: in.Nickname, Address: in.Address, ORPort: 31020, DirPort: 31021,
		V3Ident: fmt.Sprintf("%040X", 0xA0+idx), Fingerprint: fmt.Sprintf("%040X", 0xB0+idx),
		Ed25519ID: base64.RawStdEncoding.EncodeToString(append(make([]byte, 31), byte(idx+1))),
	}
}

func (s *seatFake) AuthorityKeys(_ context.Context, in AuthorityKeysInput) (tornet.AuthorityKeys, error) {
	t := s.tor
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.machine(s.ip)
	m.role = TorDirauth
	created := m.keys == nil
	if created {
		m.keys = &tornet.AuthorityKeys{
			Authority: fakeAuthority(m.idx, in), Expires: time.Now().Add(365 * 24 * time.Hour),
			Bundle: "/var/lib/orama-tor-authority/deploy/" + in.Nickname,
		}
	}
	s.w.add("tor-keys %s created=%v", s.ip, created)
	out := *m.keys
	out.Created = created
	return out, nil
}

func (s *seatFake) InstallTor(_ context.Context, in TorInstall) error {
	t := s.tor
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.failInstall[s.ip]; err != nil {
		return err
	}
	n, err := tornet.ParseNetwork(in.Network)
	if err != nil {
		return fmt.Errorf("the fake machine was given a network file it cannot parse: %w", err)
	}
	m := t.machine(s.ip)
	m.role, m.installed, m.sha, m.bootstrap = in.Role, true, netregistry.Digest(in.Network), n.Bootstrap
	if !m.firstStart.IsZero() {
		m.restartPending = true
	}
	exit := ""
	if in.Exit {
		exit = " exit"
	}
	s.w.add("tor-install %s %s bootstrap=%v%s bundle=%q node=%q", s.ip, in.Role, n.Bootstrap, exit, in.Bundle, in.NodeID)
	return nil
}

func (s *seatFake) StartTor(_ context.Context, role TorRole) error {
	t := s.tor
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.failStart[s.ip]; err != nil {
		return err
	}
	m := t.machine(s.ip)
	if m.active {
		return nil
	}
	m.active, m.startedAt, m.restartPending = true, t.now, false
	if m.firstStart.IsZero() {
		m.firstStart = t.now
	}
	s.w.add("tor-start %s %s", s.ip, role)
	return nil
}

func (s *seatFake) RestartTor(_ context.Context, role TorRole) error {
	t := s.tor
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tick()
	if err := t.failRestart[s.ip]; err != nil {
		return err
	}
	m := t.machine(s.ip)
	if role == TorDirauth && !t.noGuard {
		var recent []string
		for ip, o := range t.machines {
			if ip != s.ip && o.role == TorDirauth && t.now.Sub(o.startedAt) < simLearn {
				recent = append(recent, fmt.Sprintf("%s started %s ago", ip, t.now.Sub(o.startedAt)))
			}
		}
		if len(recent) > 0 {
			s.w.add("tor-refused %s %s", s.ip, strings.Join(recent, ","))
			return notYetError(s.ip, fmt.Sprintf("%s (an authority casts no Running vote for 30m0s after it starts): %s", tornet.AuthorityRollRefusal, strings.Join(recent, "; ")))
		}
	}
	m.startedAt, m.restartPending = t.now, false
	t.restarts = append(t.restarts, simRestart{ip: s.ip, at: t.now})
	s.w.add("tor-restart %s %s at %s", s.ip, role, t.now.Format("15:04"))
	return nil
}

func (s *seatFake) TorState(_ context.Context, role TorRole) (TorState, error) {
	t := s.tor
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tick()
	m := t.machine(s.ip)
	return TorState{
		Installed: m.installed, Active: m.active, NetworkSHA256: m.sha, RestartPending: m.restartPending,
		Now: t.now, Info: t.info(s.ip, m),
	}, nil
}
