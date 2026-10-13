package rqlite

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

// activeNamesQuery reads every name that owns an active record. It is the only
// statement that reads the table without a name to look up, and it runs on the
// backend's refresh cadence, never per DNS query.
const activeNamesQuery = `SELECT DISTINCT fqdn FROM dns_records WHERE is_active = TRUE`

// ancestorSet holds the names that have a record somewhere below them: every
// proper ancestor of every active record's name, up to but excluding the root.
//
// It answers "does anything live below this name?", which is what separates an
// empty non-terminal (the name exists, NODATA, RFC 8020) from a name that does
// not (NXDOMAIN). The question has no indexed form in SQL (it is a suffix match
// over the whole table), and the DNS query it sits behind comes from the
// internet with any name in it, so asking the database per query would be an
// unauthenticated full-table scan per distinct random name. The set is
// answered from memory instead.
//
// It holds one entry per distinct ancestor name: at most (labels per name x
// records), in practice about one per record, since siblings share ancestors.
// A million-record zone is a few hundred MB at worst; the platform's 10x
// (~10^5 names) is some tens of MB. The set is closed under "parent of": if a
// name is in it, so is every ancestor of it, which lets learn stop at the first
// ancestor it already holds.
type ancestorSet struct {
	mu    sync.RWMutex
	names map[string]struct{}
}

// has reports whether a record lives below name (a lower-case FQDN).
func (s *ancestorSet) has(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.names[name]
	return ok
}

// size is the number of names held.
func (s *ancestorSet) size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.names)
}

// learn adds the ancestors of a name that an indexed lookup just found a record
// at, so a record created since the last refresh makes its parents exist at
// once on the node that served it.
func (s *ancestorSet) learn(fqdn string) {
	parent := parentName(fqdn)
	if parent == "" {
		return
	}
	s.mu.RLock()
	_, known := s.names[parent]
	s.mu.RUnlock()
	if known {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.names == nil {
		s.names = map[string]struct{}{}
	}
	for ; parent != ""; parent = parentName(parent) {
		if _, ok := s.names[parent]; ok {
			return
		}
		s.names[parent] = struct{}{}
	}
}

// replace swaps in a freshly built set: names whose records are gone leave it.
func (s *ancestorSet) replace(names map[string]struct{}) {
	s.mu.Lock()
	s.names = names
	s.mu.Unlock()
}

// ancestorsOf is the set of proper ancestors of fqdns.
func ancestorsOf(fqdns []string) map[string]struct{} {
	names := make(map[string]struct{}, len(fqdns))
	for _, fqdn := range fqdns {
		for parent := parentName(fqdn); parent != ""; parent = parentName(parent) {
			if _, ok := names[parent]; ok {
				break
			}
			names[parent] = struct{}{}
		}
	}
	return names
}

// parentName is fqdn without its first label, or "" at the top (the root is
// not an ancestor worth keeping).
func parentName(fqdn string) string {
	next, _ := dns.NextLabel(fqdn, 0)
	if next <= 0 || next >= len(fqdn) {
		return ""
	}
	return fqdn[next:]
}

// HasBelow reports whether an active record lives below qname, as of the last
// refresh and of every record an indexed lookup has found since. A name that
// owns nothing but has such a record is an empty non-terminal: it exists.
//
// How stale it can be: a name whose only descendants were created after the
// last refresh, on a node that has not itself served one of them, answers
// NXDOMAIN (instead of NODATA) for at most one refresh period; one whose
// descendants were all removed keeps answering NODATA for the same time.
// Empty non-terminals are rare (the platform's own names are all owned or
// wildcard-covered), and the resolver-visible harm of NXDOMAIN there is bounded
// by the 30 s negative TTL on top of that.
func (b *Backend) HasBelow(qname string) bool {
	return b.below.has(dns.Fqdn(strings.ToLower(qname)))
}

// refreshAncestors rebuilds the ancestor set from the table. One scan of the
// distinct names per refresh period, whatever the query rate.
func (b *Backend) refreshAncestors(ctx context.Context) error {
	rows, err := b.client.Query(ctx, activeNamesQuery)
	if err != nil {
		return fmt.Errorf("failed to read the names the zone holds: %w", err)
	}
	fqdns := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) < 1 {
			continue
		}
		if fqdn, ok := row[0].(string); ok {
			fqdns = append(fqdns, dns.Fqdn(strings.ToLower(fqdn)))
		}
	}
	b.below.replace(ancestorsOf(fqdns))
	return nil
}
