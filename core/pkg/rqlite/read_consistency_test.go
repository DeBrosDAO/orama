package rqlite

import (
	"testing"

	"github.com/rqlite/gorqlite"
)

// feat-6: opt-in level=none reads remove the cross-region leader hop that weak
// reads pay on every query. These pin the connection-selection logic so a
// none-read can never accidentally route to the leader-bound connection (which
// would silently re-impose the 273ms hop the whole change exists to avoid).

func TestUseNoneConn(t *testing.T) {
	cases := []struct {
		name    string
		rc      ReadConsistency
		hasNone bool
		want    bool
	}{
		{"none requested + available", ReadConsistencyNone, true, true},
		{"none requested + unavailable", ReadConsistencyNone, false, false},
		{"weak requested + available", ReadConsistencyWeak, true, false},
		{"weak requested + unavailable", ReadConsistencyWeak, false, false},
		{"empty (default) + available", ReadConsistency(""), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := useNoneConn(tc.rc, tc.hasNone); got != tc.want {
				t.Errorf("useNoneConn(%q, %v) = %v; want %v", tc.rc, tc.hasNone, got, tc.want)
			}
		})
	}
}

func TestQueryConn_selectsNoneConnWhenAvailable(t *testing.T) {
	weak := &gorqlite.Connection{}
	none := &gorqlite.Connection{}
	c := &client{conn: weak, connNone: none}

	if got := c.queryConn(ReadConsistencyNone); got != none {
		t.Error("ReadConsistencyNone must select the dedicated none-level connection")
	}
	if got := c.queryConn(ReadConsistencyWeak); got != weak {
		t.Error("ReadConsistencyWeak must select the leader-routed connection")
	}
	if got := c.queryConn(ReadConsistency("")); got != weak {
		t.Error("default (empty) consistency must select the leader-routed connection")
	}
}

func TestQueryConn_degradesToWeakWhenNoneConnAbsent(t *testing.T) {
	// NewClientWithConn / NewClient build clients without a none connection.
	// A none-read must fall back to the weak conn — always correct, just
	// slower — never to a nil connection.
	weak := &gorqlite.Connection{}
	c := &client{conn: weak, connNone: nil}

	if got := c.queryConn(ReadConsistencyNone); got != weak {
		t.Error("none-read must degrade to the weak connection when connNone is nil")
	}
}
