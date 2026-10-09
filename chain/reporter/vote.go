// Package reporter turns a directory authority's archived votes into the
// MsgReportEpoch reports x/relay pays relays from (plans/open-network
// track-e E5). The observations are a pure function of the votes in the epoch
// window, so any party holding the archive recomputes the same inputs_root.
package reporter

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	// voteTimeLayout is the timestamp format of a vote header (dir-spec).
	voteTimeLayout = "2006-01-02 15:04:05"
	// maxVoteLine bounds one line of a vote; a longer one is not a vote.
	maxVoteLine = 1 << 20
	// maxVoteBytes bounds a whole vote document.
	maxVoteBytes = 64 << 20
	// maxVoteRouters bounds the relays one vote may list.
	maxVoteRouters = 1 << 17
	// fingerprintLen is the length of an RSA identity digest.
	fingerprintLen = 20
	// ed25519Len is the length of an ed25519 identity.
	ed25519Len = 32
	// minRouterFields is the field count of an r line: nickname, identity,
	// digest, date, time, address, ORPort, DirPort.
	minRouterFields = 8
)

// ErrNotAVote is a document that is not a network-status vote.
var ErrNotAVote = errors.New("not a network-status vote")

// Router is one relay's entry in one vote.
type Router struct {
	Nickname    string
	Fingerprint [fingerprintLen]byte
	// Ed25519 is empty when the vote lists the relay with "id ed25519 none"
	// or with no ed25519 line.
	Ed25519 []byte
	Flags   map[string]bool
	// Measured is the bandwidth the authority measured (the w line's Measured=
	// value, which comes from its bandwidth file). HasMeasured is false for a
	// relay the authority did not measure. The advertised Bandwidth= is never
	// read, because a relay states that figure itself.
	Measured    uint64
	HasMeasured bool
}

// Vote is the part of a vote the reporter uses.
type Vote struct {
	// Authority is the v3 identity from the dir-source line.
	Authority  [fingerprintLen]byte
	ValidAfter time.Time
	Routers    []Router
	// Digest is SHA-256 of the document, to tell a copy from a different vote.
	Digest [sha256.Size]byte
}

// ParseVote reads one complete vote document. A malformed line in a field the
// reporter reads is an error naming the line, never skipped: a vote read half way
// would change the report. A document that ends before its directory-footer is
// still being written and is refused.
func ParseVote(r io.Reader) (Vote, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxVoteBytes+1))
	if err != nil {
		return Vote{}, fmt.Errorf("read vote: %w", err)
	}
	if len(raw) > maxVoteBytes {
		return Vote{}, fmt.Errorf("vote is larger than %d bytes", maxVoteBytes)
	}
	v := Vote{Digest: sha256.Sum256(raw)}
	p := voteParser{vote: &v}
	if err := p.scan(bytes.NewReader(raw)); err != nil {
		return Vote{}, err
	}
	if !p.sawFooter {
		return Vote{}, fmt.Errorf("%w: no directory-footer line; the vote is still being written", ErrNotAVote)
	}
	if err := p.finish(); err != nil {
		return Vote{}, err
	}
	return v, nil
}

// ParseVoteHeader reads a vote's authority and valid-after and stops at the
// first relay, without reading the rest, so a directory of votes can be
// filtered cheaply. Routers and Digest are empty.
func ParseVoteHeader(r io.Reader) (Vote, error) {
	var v Vote
	p := voteParser{vote: &v, headerOnly: true}
	if err := p.scan(r); err != nil {
		return Vote{}, err
	}
	if err := p.finish(); err != nil {
		return Vote{}, err
	}
	return v, nil
}

func (p *voteParser) scan(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxVoteLine)
	for n := 1; sc.Scan(); n++ {
		if p.headerOnly && strings.HasPrefix(sc.Text(), "r ") {
			return nil
		}
		if err := p.line(sc.Text()); err != nil {
			return fmt.Errorf("vote line %d: %w", n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read vote: %w", err)
	}
	return nil
}

type voteParser struct {
	vote       *Vote
	headerOnly bool
	isVote     bool
	hasSource  bool
	sawFooter  bool
	// current indexes the router the next s, w and id lines belong to, while
	// started.
	current    int
	started    bool
	seenRouter map[[fingerprintLen]byte]struct{}
}

func (p *voteParser) router() *Router {
	if !p.started || p.current >= len(p.vote.Routers) {
		return nil
	}
	return &p.vote.Routers[p.current]
}

func (p *voteParser) line(l string) error {
	kw, rest, _ := strings.Cut(l, " ")
	switch kw {
	case "vote-status":
		p.isVote = rest == "vote"
	case "valid-after":
		t, err := time.ParseInLocation(voteTimeLayout, rest, time.UTC)
		if err != nil {
			return fmt.Errorf("valid-after %q: %w", rest, err)
		}
		p.vote.ValidAfter = t
	case "dir-source":
		return p.dirSource(rest)
	case "r":
		return p.newRouter(rest)
	case "s", "w", "id":
		return p.routerField(kw, rest)
	case "directory-footer":
		p.started, p.sawFooter = false, true
	}
	return nil
}

func (p *voteParser) dirSource(rest string) error {
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return errors.New("dir-source has no identity")
	}
	id, err := hex.DecodeString(fields[1])
	if err != nil || len(id) != fingerprintLen {
		return fmt.Errorf("dir-source identity %q is not a 40-hex digest", fields[1])
	}
	copy(p.vote.Authority[:], id)
	p.hasSource = true
	return nil
}

func (p *voteParser) newRouter(rest string) error {
	fields := strings.Fields(rest)
	if len(fields) < minRouterFields {
		return fmt.Errorf("r line has %d fields, want at least %d", len(fields), minRouterFields)
	}
	id, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(fields[1], "="))
	if err != nil || len(id) != fingerprintLen {
		return fmt.Errorf("r identity %q is not a base64 20-byte digest", fields[1])
	}
	if len(p.vote.Routers) >= maxVoteRouters {
		return fmt.Errorf("vote lists more than %d relays", maxVoteRouters)
	}
	rt := Router{Nickname: fields[0], Flags: map[string]bool{}}
	copy(rt.Fingerprint[:], id)
	if p.seenRouter == nil {
		p.seenRouter = map[[fingerprintLen]byte]struct{}{}
	}
	if _, dup := p.seenRouter[rt.Fingerprint]; dup {
		return fmt.Errorf("relay %s is listed twice", hex.EncodeToString(id))
	}
	p.seenRouter[rt.Fingerprint] = struct{}{}
	p.vote.Routers = append(p.vote.Routers, rt)
	p.current = len(p.vote.Routers) - 1
	p.started = true
	return nil
}

func (p *voteParser) routerField(kw, rest string) error {
	rt := p.router()
	if rt == nil {
		return fmt.Errorf("%s line before any r line", kw)
	}
	switch kw {
	case "s":
		for _, f := range strings.Fields(rest) {
			rt.Flags[f] = true
		}
	case "w":
		return parseWeight(rt, rest)
	case "id":
		return parseIdentity(rt, rest)
	}
	return nil
}

func parseWeight(rt *Router, rest string) error {
	for _, kv := range strings.Fields(rest) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k != "Measured" {
			continue
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return fmt.Errorf("Measured=%q: %w", v, err)
		}
		rt.Measured, rt.HasMeasured = n, true
	}
	return nil
}

func parseIdentity(rt *Router, rest string) error {
	kind, val, _ := strings.Cut(rest, " ")
	if kind != "ed25519" || val == "none" {
		return nil
	}
	id, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(val, "="))
	if err != nil || len(id) != ed25519Len {
		return fmt.Errorf("id ed25519 %q is not a base64 32-byte key", val)
	}
	rt.Ed25519 = id
	return nil
}

func (p *voteParser) finish() error {
	switch {
	case !p.isVote:
		return fmt.Errorf("%w: no \"vote-status vote\" line", ErrNotAVote)
	case !p.hasSource:
		return fmt.Errorf("%w: no dir-source line", ErrNotAVote)
	case p.vote.ValidAfter.IsZero():
		return fmt.Errorf("%w: no valid-after line", ErrNotAVote)
	}
	return nil
}
