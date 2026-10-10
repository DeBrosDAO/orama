package tornet

import (
	"strings"
	"testing"
	"time"
)

func consensusPublished(n Network, published map[int]time.Time) Consensus {
	var c Consensus
	for i, a := range n.Authorities {
		if p, ok := published[i]; ok {
			c.Relays = append(c.Relays, Relay{Nickname: a.Nickname, Fingerprint: a.Fingerprint, Published: p})
		}
	}
	return c
}

func TestRecentlyStartedAuthorities(t *testing.T) {
	n := testNetwork()
	now := time.Date(2026, 10, 10, 2, 40, 0, 0, time.UTC)
	long := now.Add(-3 * time.Hour)
	self := n.Authorities[0].Fingerprint
	cases := map[string]struct {
		published map[int]time.Time
		wantNames []string
	}{
		"every authority has run for hours":     {map[int]time.Time{0: long, 1: long, 2: long}, nil},
		"one started five minutes ago":          {map[int]time.Time{0: long, 1: now.Add(-5 * time.Minute), 2: long}, []string{"OramaAuth2"}},
		"exactly the window is long enough":     {map[int]time.Time{0: long, 1: now.Add(-AuthorityLearnWindow), 2: long}, nil},
		"a second short of the window":          {map[int]time.Time{0: long, 1: long, 2: now.Add(-AuthorityLearnWindow + time.Second)}, []string{"OramaAuth3"}},
		"one is not in the consensus":           {map[int]time.Time{0: long, 2: long}, []string{"OramaAuth2"}},
		"both others are new":                   {map[int]time.Time{0: long, 1: now.Add(-time.Minute), 2: now.Add(-2 * time.Minute)}, []string{"OramaAuth2", "OramaAuth3"}},
		"this authority's own start is ignored": {map[int]time.Time{0: now.Add(-time.Minute), 1: long, 2: long}, nil},
		"a publication time in the future":      {map[int]time.Time{0: long, 1: now.Add(time.Hour), 2: long}, []string{"OramaAuth2"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := RecentlyStartedAuthorities(n, consensusPublished(n, tc.published), self, now)
			if len(got) != len(tc.wantNames) {
				t.Fatalf("got %q, want authorities %v", got, tc.wantNames)
			}
			for i, w := range tc.wantNames {
				if !strings.HasPrefix(got[i], w+" ") {
					t.Errorf("got[%d] = %q, want it to name %s", i, got[i], w)
				}
			}
		})
	}
}

func TestParseConsensus_readsEachRelaysPublicationTime(t *testing.T) {
	c, err := ParseConsensus(strings.NewReader(string(readFixture(t, "consensus-ns.txt"))))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Relays) == 0 || c.Relays[0].Published.IsZero() {
		t.Fatalf("relays = %+v", c.Relays)
	}
	if want := time.Date(2026, 10, 8, 11, 49, 0, 0, time.UTC); !c.Relays[0].Published.Equal(want) {
		t.Errorf("published = %v, want %v", c.Relays[0].Published, want)
	}
}
