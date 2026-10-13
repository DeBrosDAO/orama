//go:build e2e_fleet

package edge

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// dnsBudget bounds one exchange with one nameserver.
const dnsBudget = 10 * time.Second

// maxUDPAnswer is the largest datagram read back.
const maxUDPAnswer = 65535

// RR is one resource record of an answer, its value rendered as text: an
// address for A, a host for NS, the strings joined for TXT, the primary and
// the minimum TTL for SOA.
type RR struct {
	Name  string
	Type  dnsmessage.Type
	TTL   uint32
	Value string
}

// Answer is one nameserver's reply.
type Answer struct {
	RCode         dnsmessage.RCode
	Authoritative bool
	Answers       []RR
	Authority     []RR
}

// Values are the answer section's values of type typ.
func (a *Answer) Values(typ dnsmessage.Type) []string {
	var out []string
	for _, rr := range a.Answers {
		if rr.Type == typ {
			out = append(out, rr.Value)
		}
	}
	return out
}

// Query asks the nameserver at server (an IP) for name/typ over network
// ("udp" or "tcp"), with recursion not desired: every answer must come from
// the server's own authority.
func Query(ctx context.Context, network, server, name string, typ dnsmessage.Type) (*Answer, error) {
	msg, err := buildQuery(name, typ)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, dnsBudget)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(server, "53"))
	if err != nil {
		return nil, fmt.Errorf("dial %s %s:53: %w", network, server, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(dl); err != nil {
			return nil, fmt.Errorf("set deadline: %w", err)
		}
	}
	raw, err := exchange(conn, network, msg)
	if err != nil {
		return nil, fmt.Errorf("%s %s @%s (%s): %w", typ, name, server, network, err)
	}
	return parseAnswer(raw)
}

func buildQuery(name string, typ dnsmessage.Type) ([]byte, error) {
	fqdn, err := dnsmessage.NewName(Fqdn(name))
	if err != nil {
		return nil, fmt.Errorf("bad DNS name %q: %w", name, err)
	}
	var idb [2]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return nil, fmt.Errorf("query id: %w", err)
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: binary.BigEndian.Uint16(idb[:])})
	if err := b.StartQuestions(); err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	if err := b.Question(dnsmessage.Question{Name: fqdn, Type: typ, Class: dnsmessage.ClassINET}); err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	return b.Finish()
}

// exchange writes msg and reads one reply, with the two-byte length prefix
// DNS over TCP uses (RFC 1035 4.2.2).
func exchange(conn net.Conn, network string, msg []byte) ([]byte, error) {
	if network == "udp" {
		if _, err := conn.Write(msg); err != nil {
			return nil, fmt.Errorf("send: %w", err)
		}
		buf := make([]byte, maxUDPAnswer)
		n, err := conn.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		return buf[:n], nil
	}
	framed := binary.BigEndian.AppendUint16(nil, uint16(len(msg)))
	if _, err := conn.Write(append(framed, msg...)); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	var lb [2]byte
	if _, err := readFull(conn, lb[:]); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	body := make([]byte, binary.BigEndian.Uint16(lb[:]))
	if _, err := readFull(conn, body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := conn.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func parseAnswer(raw []byte) (*Answer, error) {
	var p dnsmessage.Parser
	h, err := p.Start(raw)
	if err != nil {
		return nil, fmt.Errorf("parse reply: %w", err)
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, fmt.Errorf("parse questions: %w", err)
	}
	a := &Answer{RCode: h.RCode, Authoritative: h.Authoritative}
	if a.Answers, err = section(p.AnswerHeader, p.SkipAnswer, &p); err != nil {
		return nil, fmt.Errorf("parse answers: %w", err)
	}
	if a.Authority, err = section(p.AuthorityHeader, p.SkipAuthority, &p); err != nil {
		return nil, fmt.Errorf("parse authority: %w", err)
	}
	return a, nil
}

func section(next func() (dnsmessage.ResourceHeader, error), skip func() error, p *dnsmessage.Parser) ([]RR, error) {
	var out []RR
	for {
		h, err := next()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		v, err := rrValue(h, skip, p)
		if err != nil {
			return nil, err
		}
		out = append(out, RR{Name: strings.ToLower(h.Name.String()), Type: h.Type, TTL: h.TTL, Value: v})
	}
}

func rrValue(h dnsmessage.ResourceHeader, skip func() error, p *dnsmessage.Parser) (string, error) {
	switch h.Type {
	case dnsmessage.TypeA:
		r, err := p.AResource()
		return net.IP(r.A[:]).String(), err
	case dnsmessage.TypeNS:
		r, err := p.NSResource()
		return strings.ToLower(r.NS.String()), err
	case dnsmessage.TypeTXT:
		r, err := p.TXTResource()
		return strings.Join(r.TXT, ""), err
	case dnsmessage.TypeSOA:
		r, err := p.SOAResource()
		return fmt.Sprintf("%s %d", strings.ToLower(r.NS.String()), r.MinTTL), err
	default:
		return "", skip()
	}
}

// Fqdn lower-cases name and ends it with a dot.
func Fqdn(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, ".")) + "."
}
