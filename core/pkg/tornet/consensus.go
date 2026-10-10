package tornet

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// consensusTimeLayout is how dir-spec writes valid-after and its siblings, in UTC.
	consensusTimeLayout = "2006-01-02 15:04:05"
	// consensusLineLimit bounds one line of a consensus read.
	consensusLineLimit = 1 << 20

	// paramHSDirInterval is the consensus parameter that sets the length, in
	// minutes, of an onion service time period (hs_common.c).
	paramHSDirInterval = "hsdir_interval"

	flagExit    = "Exit"
	flagGuard   = "Guard"
	flagRunning = "Running"
)

// Relay is one router status entry of a consensus.
type Relay struct {
	Nickname string
	// Fingerprint is the RSA identity digest, 40 uppercase hex digits.
	Fingerprint string
	Address     string
	ORPort      int
	Flags       []string
	// Bandwidth is the consensus weight (the w line's Bandwidth=), in kilobytes per second.
	Bandwidth int64
	// Measured is true when a bandwidth authority measured the relay.
	Measured bool
	// Policy is the entry's `p` line, the summary of its exit policy that
	// clients choose exits by. Only the full ("ns") consensus carries it.
	Policy string
}

// Consensus is a parsed network-status consensus (dir-spec section 3.4).
type Consensus struct {
	// Flavor is "ns" for the full consensus and "microdesc" for the one clients fetch.
	Flavor     string
	ValidAfter time.Time
	FreshUntil time.Time
	ValidUntil time.Time
	Signatures int
	Relays     []Relay
	// Params are the integer network parameters of the params line, as the
	// authorities voted them (dir-spec 3.4.1): hsdir_interval is one.
	Params      map[string]int64
	knownFlags  []string
	versionLine string
}

// ParseConsensus reads a consensus or a vote-shaped network-status document.
// It does not verify signatures: it reads what a node holds in its
// DataDirectory (cached-consensus, cached-microdesc-consensus) to report on it,
// and an archived file is hashed, not trusted. A document missing its header
// times or router entries is an error.
func ParseConsensus(r io.Reader) (Consensus, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), consensusLineLimit)
	var c Consensus
	var cur *Relay
	flush := func() {
		if cur != nil {
			c.Relays = append(c.Relays, *cur)
			cur = nil
		}
	}
	for sc.Scan() {
		line := sc.Text()
		kw, rest, _ := strings.Cut(line, " ")
		var err error
		switch kw {
		case "network-status-version":
			c.versionLine = line
			c.Flavor = "ns"
			if f := strings.Fields(rest); len(f) == 2 {
				c.Flavor = f[1]
			}
		case "valid-after":
			c.ValidAfter, err = parseConsensusTime(rest)
		case "fresh-until":
			c.FreshUntil, err = parseConsensusTime(rest)
		case "valid-until":
			c.ValidUntil, err = parseConsensusTime(rest)
		case "known-flags":
			c.knownFlags = strings.Fields(rest)
		case "params":
			c.Params, err = parseParams(rest)
		case "directory-signature":
			c.Signatures++
		case "r":
			flush()
			var rel Relay
			if rel, err = parseRouterLine(rest); err == nil {
				cur = &rel
			}
		case "s":
			if cur != nil {
				cur.Flags = strings.Fields(rest)
			}
		case "p":
			if cur != nil {
				cur.Policy = strings.TrimSpace(rest)
			}
		case "w":
			if cur != nil {
				cur.Bandwidth, cur.Measured, err = parseWeightLine(rest)
			}
		}
		if err != nil {
			return Consensus{}, fmt.Errorf("consensus line %q: %w", line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return Consensus{}, fmt.Errorf("read the consensus: %w", err)
	}
	flush()
	if c.versionLine == "" || c.ValidAfter.IsZero() || c.FreshUntil.IsZero() || c.ValidUntil.IsZero() {
		return Consensus{}, errors.New("not a network-status document: its version line or valid-after, fresh-until or valid-until is missing")
	}
	return c, nil
}

// parseParams reads the params line: space-separated key=integer pairs.
func parseParams(rest string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, kv := range strings.Fields(rest) {
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseInt(v, 10, 64)
		if !ok || k == "" || err != nil {
			return nil, fmt.Errorf("bad network parameter %q", kv)
		}
		out[k] = n
	}
	return out, nil
}

func parseConsensusTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation(consensusTimeLayout, strings.TrimSpace(s), time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad time: %w", err)
	}
	return t, nil
}

// parseRouterLine reads a router status entry's r line. The full consensus
// ("ns") writes "nickname identity digest date time ip orport dirport" and the
// microdescriptor consensus, which has no descriptor digest on it, writes
// "nickname identity date time ip orport dirport". The address and ports are the
// last three fields in both.
func parseRouterLine(rest string) (Relay, error) {
	f := strings.Fields(rest)
	const minFields = 7
	if len(f) < minFields {
		return Relay{}, fmt.Errorf("router line has %d fields, want at least %d", len(f), minFields)
	}
	id, err := base64.RawStdEncoding.DecodeString(f[1])
	if err != nil || len(id) != fingerprintLen/2 {
		return Relay{}, errors.New("identity is not the unpadded base64 of a 20-byte digest")
	}
	n := len(f)
	port, err := strconv.Atoi(f[n-2])
	if err != nil {
		return Relay{}, fmt.Errorf("ORPort: %w", err)
	}
	return Relay{Nickname: f[0], Fingerprint: strings.ToUpper(hex.EncodeToString(id)), Address: f[n-3], ORPort: port}, nil
}

// parseWeightLine reads "Bandwidth=N [Measured=M] [Unmeasured=1]".
func parseWeightLine(rest string) (bandwidth int64, measured bool, err error) {
	for _, kv := range strings.Fields(rest) {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "Bandwidth":
			if bandwidth, err = strconv.ParseInt(v, 10, 64); err != nil || bandwidth < 0 {
				return 0, false, fmt.Errorf("bandwidth weight %q is not a number", v)
			}
		case "Measured":
			measured = true
		}
	}
	return bandwidth, measured, nil
}

// Fresh reports whether the consensus is still the one clients are told to use.
func (c Consensus) Fresh(now time.Time) bool {
	return !now.Before(c.ValidAfter) && now.Before(c.FreshUntil)
}

// Valid reports whether a client may still use the consensus.
func (c Consensus) Valid(now time.Time) bool {
	return !now.Before(c.ValidAfter) && now.Before(c.ValidUntil)
}

// Listed is the relay with fingerprint (40 hex digits), and whether the consensus lists it.
func (c Consensus) Listed(fingerprint string) (Relay, bool) {
	fp := strings.ToUpper(fingerprint)
	for _, r := range c.Relays {
		if r.Fingerprint == fp {
			return r, true
		}
	}
	return Relay{}, false
}

// Count is how many listed relays have flag.
func (c Consensus) Count(flag string) int {
	n := 0
	for _, r := range c.Relays {
		if slices.Contains(r.Flags, flag) {
			n++
		}
	}
	return n
}

// Running, Exits and Guards are the counts a status page and a launch
// threshold read.
func (c Consensus) Running() int { return c.Count(flagRunning) }
func (c Consensus) Exits() int   { return c.Count(flagExit) }
func (c Consensus) Guards() int  { return c.Count(flagGuard) }

// noExitPolicy is the summary of a policy that accepts no port.
const noExitPolicy = "reject 1-65535"

// ExitsWithoutPorts counts the relays that carry the Exit flag and whose
// policy summary accepts no port. A client takes such a relay for no exit at
// all: with only these in the consensus it reports "no exit nodes" and builds no
// path to the internet. It counts only in a consensus that carries the summary.
func (c Consensus) ExitsWithoutPorts() int {
	n := 0
	for _, r := range c.Relays {
		if r.Policy == noExitPolicy && slices.Contains(r.Flags, flagExit) {
			n++
		}
	}
	return n
}
