package rqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// RQLitePlugin implements the CoreDNS plugin interface
type RQLitePlugin struct {
	Next    plugin.Handler
	logger  *zap.Logger
	backend *Backend
	cache   *Cache
	zones   []string

	// flight collapses concurrent misses for the same name and type into one
	// resolution, so a burst of identical queries (a popular name just expired,
	// or a flood aimed at one name) costs the database one lookup, not one each.
	flight singleflight.Group

	// staleLog and failLog keep a dead backend from writing a line per query.
	staleLog logThrottle
	failLog  logThrottle
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
		return p.reply(w, r, cachedMsg)
	}

	// The shared work must not die with the caller that happened to start it.
	msg, err, _ := p.flight.Do(p.cache.key(state.Name(), state.QType()), func() (any, error) {
		return p.answer(context.WithoutCancel(ctx), &state)
	})
	if err != nil {
		return p.serveStaleOrFail(w, r, &state, err)
	}
	return p.reply(w, r, msg.(*dns.Msg).Copy())
}

// reply writes m as the answer to r. SetReply resets the rcode to success, so
// the answer's own has to be put back — otherwise an NXDOMAIN is served as an
// empty NOERROR, which is a different answer and one resolvers cache
// differently.
func (p *RQLitePlugin) reply(w dns.ResponseWriter, r *dns.Msg, m *dns.Msg) (int, error) {
	rcode := m.Rcode
	m.SetReply(r)
	m.Rcode = rcode
	w.WriteMsg(m)
	return rcode, nil
}

// answer resolves a query the cache could not answer: the records of the name,
// or the negative answer, cached with the lifetime that suits where it came
// from.
func (p *RQLitePlugin) answer(ctx context.Context, state *request.Request) (*dns.Msg, error) {
	found, err := p.lookup(ctx, state.Name(), state.QType())
	if err != nil {
		return nil, err
	}

	if len(found.records) == 0 {
		p.logger.Debug("No records found",
			zap.String("qname", state.Name()),
			zap.Uint16("qtype", state.QType()),
		)
		return p.negativeAnswer(ctx, state, found.exists)
	}

	msg := new(dns.Msg)
	msg.SetReply(state.Req)
	msg.Authoritative = true

	for _, record := range found.records {
		rr := p.buildRR(state.Name(), record)
		if rr != nil {
			msg.Answer = append(msg.Answer, rr)
		}
	}

	if found.wildcard {
		p.cache.SetWildcard(state.Name(), state.QType(), msg)
	} else {
		p.cache.Set(state.Name(), state.QType(), msg)
	}
	return msg, nil
}

// lookupResult is what the zone holds for a name and type.
type lookupResult struct {
	// records are the active records of the type asked.
	records []*DNSRecord
	// exists says the name exists although records is empty.
	exists bool
	// wildcard says records came from a wildcard standing in for the name.
	wildcard bool
}

// lookup is the records qname owns of qtype, and whether the name exists when
// it owns none. The name exists when it owns a record of another type, when a
// name below it does (an empty non-terminal, RFC 8020), or when the nearest
// wildcard that owns anything stands in for it. A name that owns a record or
// has names below it is answered from itself only; a wildcard stands in only
// for a name that does neither (RFC 4592). A miss costs the typed query and one
// query for the name and its wildcard candidates together, both indexed; what
// lives below the name is answered from memory (Backend.HasBelow).
func (p *RQLitePlugin) lookup(ctx context.Context, qname string, qtype uint16) (lookupResult, error) {
	records, err := p.backend.Query(ctx, qname, qtype)
	if err != nil || len(records) > 0 {
		return lookupResult{records: records, exists: true}, err
	}
	owners := []string{qname}
	zone := p.zoneOf(qname)
	for _, wildcardName := range p.wildcardCandidates(qname) {
		if !plugin.Name(zone).Matches(wildcardName) {
			// Past the edge of the zone qname is in: a sub-zone is not
			// answered from its parent's wildcard.
			break
		}
		owners = append(owners, wildcardName)
	}
	own, err := p.backend.Owners(ctx, owners, qtype)
	if err != nil {
		return lookupResult{}, err
	}
	// The typed query above found nothing at qname; owning any other record, or
	// having names below, makes it NODATA and keeps wildcards out of it.
	if own.Owned[strings.ToLower(qname)] || p.backend.HasBelow(qname) {
		return lookupResult{exists: true}, nil
	}
	for _, wildcardName := range owners[1:] {
		wildcardName = strings.ToLower(wildcardName)
		if len(own.Records[wildcardName]) > 0 {
			return lookupResult{records: own.Records[wildcardName], exists: true, wildcard: true}, nil
		}
		if own.Owned[wildcardName] {
			return lookupResult{exists: true, wildcard: true}, nil
		}
	}
	return lookupResult{}, nil
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

// negativeAnswer answers a query that found no record of its type, with the
// zone's own SOA in the authority section for negative caching (RFC 2308):
// NOERROR with no answer (NODATA) when the name exists with records of other
// types, NXDOMAIN when it does not exist at all.
//
// The two are not interchangeable. A resolver that gets NXDOMAIN for a
// name's AAAA concludes the name does not exist and caches that for every
// type (RFC 8020), so an AAAA query for a name with only A records made the
// name unresolvable at the resolvers that do — "no such host" for the next
// minutes, from a name that was being served.
//
// The SOA is the one the zone carries in dns_records — the nameserver
// component (pkg/node/dns_nameservers.go) writes it with the lowest glued
// slot as the primary. This used to invent one naming ns1.<first zone>
// whatever the zone, so a cluster whose ns1 slot was released, or a query in
// a second configured zone, got a negative answer signed by a nameserver that
// is not the zone's.
func (p *RQLitePlugin) negativeAnswer(ctx context.Context, state *request.Request, exists bool) (*dns.Msg, error) {
	soa, err := p.zoneSOA(ctx, p.zoneOf(state.Name()))
	if err != nil {
		return nil, err
	}

	msg := new(dns.Msg)
	rcode := dns.RcodeNameError
	if exists {
		rcode = dns.RcodeSuccess
	}
	msg.SetRcode(state.Req, rcode)
	msg.Authoritative = true
	msg.Ns = append(msg.Ns, soa)

	// Cache the negative answer, briefly.
	//
	// Without this a flood of random subdomains is a query amplifier pointed
	// straight at index rqlite: every one missed the cache and became a
	// database round trip. The TTL is short because "this name does not exist"
	// is exactly the answer most likely to be wrong soon — a namespace being
	// provisioned right now — and for the same reason a negative answer is
	// never served stale.
	p.cache.SetNegative(state.Name(), state.QType(), msg)

	return msg, nil
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

		if logIt, suppressed := p.staleLog.allow(); logIt {
			p.logger.Warn("Backend unreachable; serving a stale answer",
				zap.String("qname", state.Name()),
				zap.Uint16("qtype", state.QType()),
				zap.Duration("stale_ttl", StaleTTL),
				zap.Int("suppressed_since_last_line", suppressed),
				zap.Error(cause))
		}

		if err := w.WriteMsg(msg); err != nil {
			return dns.RcodeServerFailure, err
		}
		return dns.RcodeSuccess, nil
	}

	if logIt, suppressed := p.failLog.allow(); logIt {
		p.logger.Error("Backend query failed and nothing usable is cached",
			zap.String("qname", state.Name()),
			zap.Uint16("qtype", state.QType()),
			zap.Int("suppressed_since_last_line", suppressed),
			zap.Error(cause))
	}
	return dns.RcodeServerFailure, cause
}
