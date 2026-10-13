package ipfs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestUnderReplicated(t *testing.T) {
	tests := []struct {
		name   string
		pinned int
		rf     int
		want   bool
	}{
		{"at RF", 3, 3, false},
		{"above RF", 4, 3, false},
		{"one short", 2, 3, true},
		// The case that matters: a node was discarded and nothing re-allocated.
		{"all replicas gone", 0, 3, true},
		{"single-replica cluster", 1, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := underReplicated(&PinStatus{PinnedPeers: tc.pinned}, tc.rf); got != tc.want {
				t.Fatalf("underReplicated(%d, rf=%d) = %v, want %v", tc.pinned, tc.rf, got, tc.want)
			}
		})
	}
}

// clusterStub answers the two endpoints the sweep uses.
type clusterStub struct {
	mu       sync.Mutex
	pins     []string
	pinned   map[string]int // cid -> peers reporting "pinned"
	repinned []string
}

func (s *clusterStub) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/pins", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Newline-delimited, as ipfs-cluster streams it.
		for _, cid := range s.pins {
			fmt.Fprintf(w, `{"cid":%q,"name":"obj-%s"}`+"\n", cid, cid)
		}
	})

	mux.HandleFunc("/pins/", func(w http.ResponseWriter, r *http.Request) {
		cid := strings.TrimPrefix(r.URL.Path, "/pins/")
		s.mu.Lock()
		defer s.mu.Unlock()

		if r.Method == http.MethodPost {
			s.repinned = append(s.repinned, cid)
			fmt.Fprint(w, `{"cid":"`+cid+`"}`)
			return
		}

		peers := map[string]any{}
		for i := 0; i < s.pinned[cid]; i++ {
			peers[fmt.Sprintf("peer-%d", i)] = map[string]any{"status": "pinned"}
		}
		// One peer that is still fetching, to prove it is not counted.
		peers["peer-pinning"] = map[string]any{"status": "pinning"}

		body := map[string]any{"cid": cid, "name": "obj-" + cid, "peer_map": peers}
		writeJSONBody(w, body)
	})

	return mux
}

func writeJSONBody(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func TestRepinUnderReplicated_repinsOnlyWhatIsShort(t *testing.T) {
	stub := &clusterStub{
		pins:   []string{"cid-healthy", "cid-short", "cid-empty"},
		pinned: map[string]int{"cid-healthy": 3, "cid-short": 1, "cid-empty": 0},
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := &Client{apiURL: srv.URL, httpClient: srv.Client()}

	result, err := c.RepinUnderReplicated(context.Background(), 3)
	if err != nil {
		t.Fatalf("RepinUnderReplicated: %v", err)
	}

	if result.Examined != 3 {
		t.Fatalf("examined = %d, want 3", result.Examined)
	}
	if result.UnderReplicated != 2 || result.Repinned != 2 {
		t.Fatalf("under=%d repinned=%d, want 2 and 2", result.UnderReplicated, result.Repinned)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	for _, cid := range stub.repinned {
		if cid == "cid-healthy" {
			t.Fatal("a CID already at its replication factor was re-pinned; " +
				"re-pinning everything churns allocations across the whole cluster")
		}
	}
	if len(stub.repinned) != 2 {
		t.Fatalf("re-pinned %v, want the two short ones", stub.repinned)
	}
}

func TestRepinUnderReplicated_doesNotCountPinningAsAReplica(t *testing.T) {
	// A peer that is still fetching has not got the content. Counting it is how
	// a CID one failure from loss reads as healthy.
	stub := &clusterStub{pins: []string{"cid-a"}, pinned: map[string]int{"cid-a": 2}}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := &Client{apiURL: srv.URL, httpClient: srv.Client()}

	// The stub always adds one "pinning" peer, so peer_map has 3 entries but
	// only 2 are actually pinned.
	result, err := c.RepinUnderReplicated(context.Background(), 3)
	if err != nil {
		t.Fatalf("RepinUnderReplicated: %v", err)
	}
	if result.UnderReplicated != 1 {
		t.Fatalf("under = %d, want 1 — the 'pinning' peer must not count as a replica", result.UnderReplicated)
	}
}

func TestRepinUnderReplicated_oneFailureDoesNotStopTheSweep(t *testing.T) {
	// The next CID may be the one that is a single failure from being lost.
	mux := http.NewServeMux()
	mux.HandleFunc("/pins", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"cid":"cid-bad","name":"a"}`+"\n"+`{"cid":"cid-good","name":"b"}`+"\n")
	})
	repinned := 0
	var mu sync.Mutex
	mux.HandleFunc("/pins/", func(w http.ResponseWriter, r *http.Request) {
		cid := strings.TrimPrefix(r.URL.Path, "/pins/")
		if cid == "cid-bad" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodPost {
			mu.Lock()
			repinned++
			mu.Unlock()
			fmt.Fprint(w, `{"cid":"`+cid+`"}`)
			return
		}
		writeJSONBody(w, map[string]any{"cid": cid, "name": "b", "peer_map": map[string]any{}})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{apiURL: srv.URL, httpClient: srv.Client()}

	result, err := c.RepinUnderReplicated(context.Background(), 3)
	if err != nil {
		t.Fatalf("RepinUnderReplicated: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("failed = %d, want 1", result.Failed)
	}
	mu.Lock()
	defer mu.Unlock()
	if repinned != 1 {
		t.Errorf("the sweep stopped at the first failure; re-pinned %d", repinned)
	}
}

func TestListPinnedCIDs_readsANewlineDelimitedStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"cid":"a","name":"one"}`+"\n"+`{"cid":"b","name":"two"}`+"\n")
	}))
	defer srv.Close()

	c := &Client{apiURL: srv.URL, httpClient: srv.Client()}
	pins, err := c.ListPinnedCIDs(context.Background())
	if err != nil {
		t.Fatalf("ListPinnedCIDs: %v", err)
	}
	if len(pins) != 2 || pins[0].Cid != "a" || pins[1].Cid != "b" {
		t.Fatalf("got %+v", pins)
	}
}
