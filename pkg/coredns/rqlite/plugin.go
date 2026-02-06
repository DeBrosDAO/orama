package rqlite

import (
	"context"
	"time"

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
	if cachedMsg := p.cache.Get(state.Name(), state.QType()); cachedMsg != nil {
		p.logger.Debug("Cache hit",
			zap.String("qname", state.Name()),
			zap.Uint16("qtype", state.QType()),
		)
		cachedMsg.SetReply(r)
		w.WriteMsg(cachedMsg)
		return dns.RcodeSuccess, nil
	}

	// Query RQLite backend
	records, err := p.backend.Query(ctx, state.Name(), state.QType())
	if err != nil {
		p.logger.Error("Backend query failed",
			zap.String("qname", state.Name()),
			zap.Error(err),
		)
		return dns.RcodeServerFailure, err
	}

	// If no exact match, try wildcard
	if len(records) == 0 {
		wildcardName := p.getWildcardName(state.Name())
		if wildcardName != "" {
			records, err = p.backend.Query(ctx, wildcardName, state.QType())
			if err != nil {
				p.logger.Error("Wildcard query failed",
					zap.String("wildcard", wildcardName),
					zap.Error(err),
				)
				return dns.RcodeServerFailure, err
			}
		}
	}

	// No records found
	if len(records) == 0 {
		p.logger.Debug("No records found",
			zap.String("qname", state.Name()),
			zap.Uint16("qtype", state.QType()),
		)
		return p.handleNXDomain(ctx, w, r, &state)
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

// getWildcardName extracts the wildcard pattern for a given name
// e.g., myapp.node-7prvNa.orama.network -> *.node-7prvNa.orama.network
func (p *RQLitePlugin) getWildcardName(qname string) string {
	labels := dns.SplitDomainName(qname)
	if len(labels) < 3 {
		return ""
	}

	// Replace first label with wildcard
	labels[0] = "*"
	return dns.Fqdn(dns.Fqdn(labels[0] + "." + labels[1] + "." + labels[2]))
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

// handleNXDomain handles the case where no records are found
func (p *RQLitePlugin) handleNXDomain(ctx context.Context, w dns.ResponseWriter, r *dns.Msg, state *request.Request) (int, error) {
	msg := new(dns.Msg)
	msg.SetRcode(r, dns.RcodeNameError)
	msg.Authoritative = true

	// Add SOA record for negative caching
	soa := &dns.SOA{
		Hdr: dns.RR_Header{
			Name:   p.zones[0],
			Rrtype: dns.TypeSOA,
			Class:  dns.ClassINET,
			Ttl:    300,
		},
		Ns:      "ns1." + p.zones[0],
		Mbox:    "admin." + p.zones[0],
		Serial:  uint32(time.Now().Unix()),
		Refresh: 3600,
		Retry:   600,
		Expire:  86400,
		Minttl:  300,
	}
	msg.Ns = append(msg.Ns, soa)

	w.WriteMsg(msg)
	return dns.RcodeNameError, nil
}

// Ready implements the ready.Readiness interface
func (p *RQLitePlugin) Ready() bool {
	return p.backend.Healthy()
}
