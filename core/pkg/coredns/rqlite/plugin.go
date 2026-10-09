package rqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"go.uber.org/zap"
)

// RQLitePlugin implements the CoreDNS plugin interface
type RQLitePlugin struct {
	Next    plugin.Handler
	logger  *zap.Logger
	backend *Backend
	cache   *Cache
	zones   []string
}

// Name returns the plugin name
func (p *RQLitePlugin) Name() string {
	return "rqlite"
}

// ServeDNS implements the plugin.Handler interface
func (p *RQLitePlugin) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}

	// Only handle queries for our configured zones
	if !p.isOurZone(state.Name()) {
		return plugin.NextOrFailure(p.Name(), p.Next, ctx, w, r)
	}

	// Check cache first
	if cachedMsg, negative := p.cache.Get(state.Name(), state.QType()); cachedMsg != nil {
		p.logger.Debug("Cache hit",
			zap.String("qname", state.Name()),
			zap.Uint16("qtype", state.QType()),
			zap.Bool("negative", negative),
		)
		// SetReply resets the rcode to success, so a cached NXDOMAIN has to
		// have it put back — otherwise it is served as an empty NOERROR, which
		// is a different answer and one resolvers cache differently. A cached
		// NODATA is NOERROR already, so the stored rcode is the right one for
		// both.
		rcode := cachedMsg.Rcode
		cachedMsg.SetReply(r)
		cachedMsg.Rcode = rcode
		w.WriteMsg(cachedMsg)
		return rcode, nil
	}

	// Query RQLite backend
	records, err := p.backend.Query(ctx, state.Name(), state.QType())
	if err != nil {
		return p.serveStaleOrFail(w, r, &state, err)
	}

	// If no exact match, walk the wildcards outward.
	wildcards := p.zoneWildcards(state.Name())
	if len(records) == 0 {
		for _, wildcardName := range wildcards {
			records, err = p.backend.Query(ctx, wildcardName, state.QType())
			if err != nil {
				return p.serveStaleOrFail(w, r, &state, err)
			}
			if len(records) > 0 {
				break
			}
		}
	}

	// No records of this type
	if len(records) == 0 {
		p.logger.Debug("No records found",
			zap.String("qname", state.Name()),
			zap.Uint16("qtype", state.QType()),
		)
		exists, err := p.backend.NameExists(ctx, state.Name(), wildcards)
		if err != nil {
			return p.serveStaleOrFail(w, r, &state, err)
		}
		return p.handleNegative(ctx, w, r, &state, exists)
	}

	// Build response
	msg := new(dns.Msg)
	msg.SetReply(r)
	msg.Authoritative = true

	for _, record := range records {
		rr := p.buildRR(state.Name(), record)
		if rr != nil {
			msg.Answer = append(msg.Answer, rr)
		}
	}

	// Cache the response
	p.cache.Set(state.Name(), state.QType(), msg)

	w.WriteMsg(msg)
	return dns.RcodeSuccess, nil
}

// isOurZone checks if the query is for one of our configured zones
func (p *RQLitePlugin) isOurZone(qname string) bool {
	for _, zone := range p.zones {
		if plugin.Name(zone).Matches(qname) {
			return true
		}
	}
	return false
}

// zoneWildcards is wildcardCandidates cut at the edge of what this server is
// authoritative for: a wildcard outside the zones is not ours to answer from.
func (p *RQLitePlugin) zoneWildcards(qname string) []string {
	candidates := p.wildcardCandidates(qname)
	for i, name := range candidates {
		if !p.isOurZone(name) {
			return candidates[:i]
		}
	}
	return candidates
}

// wildcardCandidates returns the wildcard names that could match qname, from
// most to least specific.
//
// Standard wildcard semantics: for a.b.c.d. the candidates are *.b.c.d., then
// *.c.d., then *.d. — each label is dropped in turn and replaced by a wildcard
// one level up.
//
// This used to build `"*." + labels[1] + "." + labels[2]` and stop, which is
// only correct for a three-label name. For x.ns-anchat.orama-devnet.network. it
// produced `*.ns-anchat.orama-devnet.` — the TLD dropped — so it matched none
// of the `*.ns-<ns>.<base>.` rows CreateNamespaceRecords writes, and every
// per-namespace sub-name (including turn.ns-<ns>.<base>) was unresolvable.
func (p *RQLitePlugin) wildcardCandidates(qname string) []string {
	labels := dns.SplitDomainName(qname)
	if len(labels) < 2 {
		return nil
	}

	candidates := make([]string, 0, len(labels)-1)
	// Stop before the last label: "*." alone is not a name worth querying, and
	// a wildcard at the TLD is never something this zone owns.
	for i := 1; i < len(labels); i++ {
		candidates = append(candidates, dns.Fqdn("*."+strings.Join(labels[i:], ".")))
	}
	return candidates
}

// buildRR builds a DNS resource record from a DNSRecord
func (p *RQLitePlugin) buildRR(qname string, record *DNSRecord) dns.RR {
	header := dns.RR_Header{
		Name:   qname,
		Rrtype: record.Type,
		Class:  dns.ClassINET,
		Ttl:    uint32(record.TTL),
	}

	switch record.Type {
	case dns.TypeA:
		return &dns.A{
			Hdr: header,
			A:   record.ParsedValue.(*dns.A).A,
		}
	case dns.TypeAAAA:
		return &dns.AAAA{
			Hdr:  header,
			AAAA: record.ParsedValue.(*dns.AAAA).AAAA,
		}
	case dns.TypeCNAME:
		return &dns.CNAME{
			Hdr:    header,
			Target: record.ParsedValue.(string),
		}
	case dns.TypeTXT:
		return &dns.TXT{
			Hdr: header,
			Txt: record.ParsedValue.([]string),
		}
	case dns.TypeNS:
		return &dns.NS{
			Hdr: header,
			Ns:  record.ParsedValue.(string),
		}
	case dns.TypeSOA:
		soa := record.ParsedValue.(*dns.SOA)
		soa.Hdr = header
		return soa
	default:
		p.logger.Warn("Unsupported record type",
			zap.Uint16("type", record.Type),
		)
		return nil
	}
}

// handleNegative answers a name that has no record of the type asked, with the
// zone's own SOA in the authority section for negative caching (RFC 2308).
//
// exists says whether the name has anything at all — a record of another type,
// a wildcard that covers it, or names below it. If it does the answer is NODATA
// (NOERROR, empty); only a name that is absent altogether is NXDOMAIN.
//
// That distinction is not cosmetic. NXDOMAIN says the NAME does not exist, for
// every type, and a validating or qname-minimising resolver (RFC 8020, RFC 9156)
// acts on it: it answers NXDOMAIN to the A query too, for the whole negative TTL
// and for everything below the name. This used to answer NXDOMAIN to the AAAA,
// NS and TXT queries resolvers send alongside A, so a namespace's
// ns-<name>.<base> resolved for a minute and then, once a resolver had asked
// for its NS or AAAA, did not resolve at all ("no such host") until the SOA's
// negative TTL ran out.
//
// The SOA is the one the zone carries in dns_records — the nameserver
// component (pkg/node/dns_nameservers.go) writes it with the lowest glued
// slot as the primary. This used to invent one naming ns1.<first zone>
// whatever the zone, so a cluster whose ns1 slot was released, or a query in
// a second configured zone, got a negative answer signed by a nameserver that
// is not the zone's.
func (p *RQLitePlugin) handleNegative(ctx context.Context, w dns.ResponseWriter, r *dns.Msg, state *request.Request, exists bool) (int, error) {
	zone := p.zoneOf(state.Name())
	soa, err := p.zoneSOA(ctx, zone)
	if err != nil {
		return p.serveStaleOrFail(w, r, state, err)
	}

	rcode := dns.RcodeNameError
	if exists {
		rcode = dns.RcodeSuccess
	}
	msg := new(dns.Msg)
	msg.SetRcode(r, rcode)
	msg.Authoritative = true
	msg.Ns = append(msg.Ns, soa)

	// Cache the negative answer, briefly.
	//
	// Without this a flood of random subdomains is a query amplifier pointed
	// straight at index rqlite: every one missed the cache and became a
	// database round trip. The TTL is short because "this name does not exist"
	// (or "has no such type") is exactly the answer most likely to be wrong
	// soon — a namespace being provisioned right now — and for the same reason
	// a negative answer is never served stale.
	p.cache.SetNegative(state.Name(), state.QType(), msg)

	w.WriteMsg(msg)
	return rcode, nil
}

// zoneOf is the most specific configured zone qname is in.
func (p *RQLitePlugin) zoneOf(qname string) string {
	return plugin.Zones(p.zones).Matches(qname)
}

// zoneSOA is zone's SOA record as the negative-answer authority: owned by the
// apex, with the TTL RFC 2308 gives it — the lesser of the record's TTL and
// its minimum field. A zone without one is an error rather than something to
// make up: its nameserver component has not published it yet (no slot is
// claimed and glued), and an invented SOA would name a primary that may not
// exist.
func (p *RQLitePlugin) zoneSOA(ctx context.Context, zone string) (*dns.SOA, error) {
	records, err := p.backend.Query(ctx, zone, dns.TypeSOA)
	if err != nil {
		return nil, fmt.Errorf("read the SOA of zone %s: %w", zone, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("zone %s has no SOA record in dns_records; a nameserver writes it once it holds a glued "+
			"slot in dns_nameservers — check that orama-node is running on the nameservers", zone)
	}
	stored := records[0].ParsedValue.(*dns.SOA)
	soa := *stored
	soa.Hdr = dns.RR_Header{
		Name:   zone,
		Rrtype: dns.TypeSOA,
		Class:  dns.ClassINET,
		Ttl:    min(uint32(records[0].TTL), stored.Minttl),
	}
	return &soa, nil
}

// Ready implements the ready.Readiness interface
func (p *RQLitePlugin) Ready() bool {
	return p.backend.Healthy()
}

// serveStaleOrFail answers from the stale cache when the backend is
// unreachable, and only SERVFAILs when there is genuinely nothing to say.
//
// This is the difference between "the database is down" and "the zone is gone".
// Every backend error used to become SERVFAIL for the whole zone, so an index
// rqlite with no leader took every name in the fleet offline — including the
// names an operator needs to reach the machines and fix it.
func (p *RQLitePlugin) serveStaleOrFail(w dns.ResponseWriter, r *dns.Msg, state *request.Request, cause error) (int, error) {
	if msg := p.cache.GetStale(state.Name(), state.QType()); msg != nil {
		msg.SetReply(r)
		msg.Authoritative = true

		p.logger.Warn("Backend unreachable; serving a stale answer",
			zap.String("qname", state.Name()),
			zap.Uint16("qtype", state.QType()),
			zap.Duration("stale_ttl", StaleTTL),
			zap.Error(cause))

		if err := w.WriteMsg(msg); err != nil {
			return dns.RcodeServerFailure, err
		}
		return dns.RcodeSuccess, nil
	}

	p.logger.Error("Backend query failed and nothing usable is cached",
		zap.String("qname", state.Name()),
		zap.Uint16("qtype", state.QType()),
		zap.Error(cause))
	return dns.RcodeServerFailure, cause
}
