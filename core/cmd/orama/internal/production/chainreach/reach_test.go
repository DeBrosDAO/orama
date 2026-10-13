package chainreach

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestParseProbe(t *testing.T) {
	tests := []struct {
		out     string
		want    Probe
		wantErr bool
	}{
		{"yes yes\n", Probe{Chain: true, Colocated: true}, false},
		{"yes no\n", Probe{Chain: true}, false},
		{"no no\n", Probe{}, false},
		{"", Probe{}, true},
		{"yes\n", Probe{}, true},
		{"maybe no\n", Probe{}, true},
		{"yes no extra\n", Probe{}, true},
	}
	for _, tt := range tests {
		got, err := parseProbe(tt.out)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseProbe(%q) = %+v, %v; want %+v, error %v", tt.out, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestProbe_RESTAddrFollowsTheNamespace(t *testing.T) {
	if got := (Probe{Colocated: true}).RESTAddr(); got != "198.18.0.2:31003" {
		t.Errorf("co-located addr = %s", got)
	}
	if got := (Probe{}).RESTAddr(); got != "127.0.0.1:31003" {
		t.Errorf("global-only addr = %s", got)
	}
	if got := (Probe{Colocated: true}).RPCAddr(); got != "198.18.0.2:31001" {
		t.Errorf("co-located rpc addr = %s", got)
	}
}

// fakeRunner answers probes from answers and records the tunnels it opens.
type fakeRunner struct {
	answers  map[string]string
	probeErr map[string]error
	tunnelTo []string
	closed   []string
	failOn   string
}

func (f *fakeRunner) runner() Runner {
	return Runner{
		Output: func(n inspector.Node, _ string) (string, error) {
			if err := f.probeErr[n.Host]; err != nil {
				return "", err
			}
			return f.answers[n.Host], nil
		},
		Tunnel: func(_ context.Context, n inspector.Node, remote string) (string, func() error, error) {
			if n.Host == f.failOn {
				return "", nil, errors.New("tunnel refused")
			}
			f.tunnelTo = append(f.tunnelTo, n.Host+"->"+remote)
			return "127.0.0.1:40000", func() error { f.closed = append(f.closed, n.Host); return nil }, nil
		},
	}
}

func nodes(hosts ...string) []inspector.Node {
	var out []inspector.Node
	for _, h := range hosts {
		out = append(out, inspector.Node{Host: h, User: "root"})
	}
	return out
}

func TestOpen_usesTheFirstNodeThatRunsTheChain(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"a": "no no\n", "b": "yes yes\n", "c": "yes no\n"}}

	reach, err := f.runner().Open(context.Background(), nodes("a", "b", "c"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if reach.Node.Host != "b" || reach.Base != "http://127.0.0.1:40000" {
		t.Errorf("reach = %+v, want node b", reach)
	}
	if want := []string{"b->198.18.0.2:31003", "b->198.18.0.2:31001"}; !slices.Equal(f.tunnelTo, want) {
		t.Errorf("tunnels = %v, want the REST API and the RPC of b's namespace address %v", f.tunnelTo, want)
	}
	if err := reach.Close(); err != nil || len(f.closed) != 2 {
		t.Errorf("Close = %v, closed %v: both forwards must close", err, f.closed)
	}
}

func TestOpen_skipsANodeItCannotAskOrReach(t *testing.T) {
	f := &fakeRunner{
		answers:  map[string]string{"b": "yes no\n", "c": "yes no\n"},
		probeErr: map[string]error{"a": errors.New("ssh timeout")},
		failOn:   "b",
	}

	reach, err := f.runner().Open(context.Background(), nodes("a", "b", "c"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if reach.Node.Host != "c" {
		t.Errorf("reached %s, want c after a and b failed", reach.Node.Host)
	}
}

func TestOpen_noNodeRunsTheChain(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"a": "no no\n"}}

	_, err := f.runner().Open(context.Background(), nodes("a"))

	if err == nil || !strings.Contains(err.Error(), "none of the nodes runs the chain") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpen_everyFailureIsReported(t *testing.T) {
	f := &fakeRunner{probeErr: map[string]error{"a": errors.New("ssh timeout")}}

	_, err := f.runner().Open(context.Background(), nodes("a"))

	if err == nil || !strings.Contains(err.Error(), "ssh timeout") {
		t.Fatalf("err = %v, want the probe failure kept", err)
	}
}

func restReach(t *testing.T, body string) *Reach {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != nodeInfoPath {
			t.Errorf("read %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Reach{Node: inspector.Node{Host: "h"}, Base: srv.URL}
}

func TestReach_ChainID(t *testing.T) {
	r := restReach(t, `{"default_node_info":{"network":"orama-stagenet-6"}}`)

	got, err := r.ChainID(context.Background())

	if err != nil || got != "orama-stagenet-6" {
		t.Fatalf("ChainID = %q, %v", got, err)
	}
}

func TestReach_ChainIDWithoutANetworkIsAnError(t *testing.T) {
	r := restReach(t, `{"default_node_info":{}}`)

	if _, err := r.ChainID(context.Background()); err == nil || !strings.Contains(err.Error(), "without a chain id") {
		t.Fatalf("err = %v", err)
	}
}

func TestReach_CheckChain_refusesANodeOnAnotherChain(t *testing.T) {
	r := restReach(t, `{"default_node_info":{"network":"orama-evil-1"}}`)

	err := r.CheckChain(context.Background(), "orama-stagenet-6")

	if err == nil || !strings.Contains(err.Error(), "refusing to sign") || !strings.Contains(err.Error(), "orama-evil-1") {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.Client(context.Background(), nil, "orama-stagenet-6"); err == nil || !strings.Contains(err.Error(), "refusing to sign") {
		t.Errorf("Client built for a chain the node does not run: %v", err)
	}
}

func TestReach_CheckChain_acceptsTheChainTheNetworkRuns(t *testing.T) {
	r := restReach(t, `{"default_node_info":{"network":"orama-stagenet-6"}}`)

	if err := r.CheckChain(context.Background(), "orama-stagenet-6"); err != nil {
		t.Fatal(err)
	}
}

func TestReach_CheckChain_needsAChainIDToCompareWith(t *testing.T) {
	r := restReach(t, `{"default_node_info":{"network":"orama-stagenet-6"}}`)

	if err := r.CheckChain(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "--chain-id") {
		t.Fatalf("err = %v: the node's own word for its chain is not a pin", err)
	}
}

func TestPin(t *testing.T) {
	registry := map[string]string{"stagenet": "orama-stagenet-6"}
	old := expectedChainID
	t.Cleanup(func() { expectedChainID = old })
	expectedChainID = func(env string) (string, string, error) {
		if id, ok := registry[env]; ok {
			return id, env, nil
		}
		return "", "", nil
	}
	for name, tc := range map[string]struct {
		env, explicit, want, wantErr string
	}{
		"the registry names it":              {"stagenet", "", "orama-stagenet-6", ""},
		"the flag agrees":                    {"stagenet", "orama-stagenet-6", "orama-stagenet-6", ""},
		"the flag disagrees":                 {"stagenet", "orama-other-1", "", "is not the chain of network"},
		"no registry network, the flag does": {"mine", "orama-mine-1", "orama-mine-1", ""},
		"no registry network, no flag":       {"mine", "", "", "--chain-id"},
		"a flag that is no chain id":         {"mine", "Orama 1", "", "is not a chain id"},
	} {
		got, err := Pin(tc.env, tc.explicit)
		if tc.wantErr == "" && (err != nil || got != tc.want) {
			t.Errorf("%s: %q, %v; want %q", name, got, err, tc.want)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: err = %v, want it to say %q", name, err, tc.wantErr)
		}
	}
	expectedChainID = func(string) (string, string, error) { return "", "", errors.New("store unreadable") }
	if _, err := Pin("stagenet", ""); err == nil || !strings.Contains(err.Error(), "store unreadable") {
		t.Errorf("a registry that cannot be read is an error with its cause: %v", err)
	}
}
