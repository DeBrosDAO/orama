package setup

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/releasefetch"
)

// ---- releaseFetcher

func TestReleaseFetcher_fetchesVerifiesAndEndorses(t *testing.T) {
	home := t.TempDir()
	var params releasefetch.Params
	var endorsedRoot []byte
	f := releaseFetcher{
		fetch: func(_ context.Context, p releasefetch.Params) (*releasefetch.Release, error) {
			params = p
			archive := filepath.Join(p.WorkDir, "release.tar.gz")
			return &releasefetch.Release{Version: "0.3.1", ArchivePath: archive, Root: []byte("rotated-root")}, nil
		},
		endorse: func(src, dst string, root []byte) error {
			endorsedRoot = root
			return os.WriteFile(dst, []byte("archive"), 0o600)
		},
		now:  func() time.Time { return time.Unix(1_800_000_000, 0) },
		home: func() (string, error) { return home, nil },
	}
	n := &netregistry.Network{Root: []byte("embedded-root"), Manifest: &netregistry.Manifest{
		Name: "stagenet", Channel: "nightly", ReleaseRepo: "https://releases.example", ReleaseRootSHA256: testRootSHA, MinVersion: "0.3.0",
	}}
	_, err := f.Fetch(context.Background(), n, "amd64")
	// The endorsed file is not a real archive, so reading its manifest fails: what
	// matters is what was asked and what was signed.
	if err == nil || !strings.Contains(err.Error(), "archive") {
		t.Fatalf("got %v", err)
	}
	if params.RepoURL != "https://releases.example" || params.Channel != "nightly" || params.Arch != "amd64" || params.MinVersion != "0.3.0" || params.RootSHA256 != testRootSHA {
		t.Errorf("params %+v: the manifest's pins must reach the fetch", params)
	}
	if string(params.Root) != "embedded-root" {
		t.Errorf("the fetch starts from the root built into the CLI, got %q", params.Root)
	}
	if params.SeenPath != filepath.Join(home, "release-seen.json") || params.AdoptedRoot != filepath.Join(home, "release-root-adopted.json") {
		t.Errorf("rollback records %q %q", params.SeenPath, params.AdoptedRoot)
	}
	if string(endorsedRoot) != "rotated-root" {
		t.Errorf("the wallet signs the manifest with the root the release verified under, got %q", endorsedRoot)
	}
	if _, statErr := os.Stat(params.WorkDir); !os.IsNotExist(statErr) {
		t.Error("a failed fetch removes its working directory")
	}
}

func TestReleaseFetcher_failedFetchOrEndorsementCleansUp(t *testing.T) {
	var work string
	f := releaseFetcher{
		fetch: func(_ context.Context, p releasefetch.Params) (*releasefetch.Release, error) {
			work = p.WorkDir
			return nil, errors.New("the channel lists no release")
		},
		now:  time.Now,
		home: func() (string, error) { return t.TempDir(), nil },
	}
	n := &netregistry.Network{Manifest: &netregistry.Manifest{Channel: "nightly"}}
	if _, err := f.Fetch(context.Background(), n, "amd64"); err == nil || !strings.Contains(err.Error(), "no release") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Error("the working directory is removed")
	}
}

func TestReleaseFetcher_homeFailureIsReturned(t *testing.T) {
	f := releaseFetcher{home: func() (string, error) { return "", errors.New("no home") }}
	if _, err := f.Fetch(context.Background(), &netregistry.Network{Manifest: &netregistry.Manifest{}}, "amd64"); err == nil {
		t.Fatal("want an error")
	}
}

// ---- registryNetworks

func testRegistry(t *testing.T, names ...string) func() (*netregistry.Registry, error) {
	t.Helper()
	fsys := fstest.MapFS{}
	for _, name := range names {
		root := []byte("release-root-" + name)
		m := netregistry.Manifest{
			Name: name, ChainID: "orama-" + name + "-1", GenesisSHA256: strings.Repeat("0", 64),
			Seeds: []string{"seed1." + name + ".example", "seed2." + name + ".example"}, Channel: "nightly", MinVersion: "0.3.0",
			ReleaseRepo: "https://releases.example", ReleaseRootSHA256: netregistry.Digest(root),
		}
		data, err := m.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		fsys["registry/"+name+"/manifest.json"] = &fstest.MapFile{Data: data}
		fsys["registry/"+name+"/release-root.json"] = &fstest.MapFile{Data: root}
	}
	if len(names) == 0 {
		fsys["registry/.keep"] = &fstest.MapFile{}
	}
	reg, err := netregistry.LoadFS(fsys, "registry")
	if err != nil {
		t.Fatal(err)
	}
	return func() (*netregistry.Registry, error) { return reg, nil }
}

func TestRegistryNetworks_defaultsAndRefusals(t *testing.T) {
	r := registryNetworks{
		load: testRegistry(t, "stagenet"), active: func() (string, error) { return "", errors.New("none") },
	}
	n, err := r.Resolve(context.Background(), "")
	if err != nil || n.Manifest.Name != "stagenet" {
		t.Fatalf("the only network is the default: %v, %v", n, err)
	}
	if _, err := r.Resolve(context.Background(), "mainnet"); err == nil || !strings.Contains(err.Error(), "stagenet") {
		t.Errorf("an unknown network lists the known ones: %v", err)
	}
	if _, err := r.Resolve(context.Background(), "https://example.org/manifest.json"); err == nil || !strings.Contains(err.Error(), "orama network add") {
		t.Errorf("a URL goes through `network add` first: %v", err)
	}
	two := registryNetworks{load: testRegistry(t, "stagenet", "testnet"), active: func() (string, error) { return "", errors.New("none") }}
	if _, err := two.Resolve(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "--network") {
		t.Errorf("two networks and no choice: %v", err)
	}
	active := registryNetworks{load: testRegistry(t, "stagenet", "testnet"), active: func() (string, error) { return "testnet", nil }}
	if n, err := active.Resolve(context.Background(), ""); err != nil || n.Manifest.Name != "testnet" {
		t.Errorf("the active network wins: %v, %v", n, err)
	}
	none := registryNetworks{load: testRegistry(t), active: func() (string, error) { return "", errors.New("none") }}
	if _, err := none.Resolve(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "knows no network") {
		t.Errorf("an empty registry: %v", err)
	}
}

// ---- ASN

func TestOriginASN(t *testing.T) {
	defer func(old func(context.Context, string) ([]string, error)) { lookupTXT = old }(lookupTXT)
	var asked string
	lookupTXT = func(_ context.Context, name string) ([]string, error) {
		asked = name
		return []string{"24940 | 203.0.113.0/24 | DE | ripencc | 2010-01-01"}, nil
	}
	asn, err := OriginASN(context.Background(), "203.0.113.11")
	if err != nil || asn != 24940 {
		t.Fatalf("%d, %v", asn, err)
	}
	if asked != "11.113.0.203.origin.asn.cymru.com" {
		t.Errorf("asked %q: the octets are reversed", asked)
	}
}

func TestOriginASN_refusals(t *testing.T) {
	defer func(old func(context.Context, string) ([]string, error)) { lookupTXT = old }(lookupTXT)
	for name, records := range map[string][]string{
		"no record":         nil,
		"a private ASN":     {"64600 | 10.0.0.0/8 | ZZ | x | y"},
		"not a number":      {"abc | 1.0.0.0/8"},
		"an empty record":   {" | 1.0.0.0/8"},
		"the documentation": {"64500 | x"},
	} {
		lookupTXT = func(context.Context, string) ([]string, error) { return records, nil }
		if _, err := OriginASN(context.Background(), "203.0.113.11"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	lookupTXT = func(context.Context, string) ([]string, error) { return nil, errors.New("timeout") }
	if _, err := OriginASN(context.Background(), "203.0.113.11"); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("a resolver failure: %v", err)
	}
	if _, err := OriginASN(context.Background(), "2001:db8::1"); err == nil {
		t.Error("IPv6 has no answer here")
	}
}

func TestParseCymru_firstOfSeveral(t *testing.T) {
	if asn, err := parseCymru("13335 15169 | 1.1.1.0/24 | AU"); err != nil || asn != 13335 {
		t.Fatalf("%d, %v", asn, err)
	}
}

// ---- faucet

// seedDoer answers each seed's faucet request from a script and records the order they were asked in.
type seedDoer struct {
	answers map[string]seedAnswer
	asked   []string
	body    string
}

type seedAnswer struct {
	status int
	body   string
	err    error
}

func (d *seedDoer) Do(req *http.Request) (*http.Response, error) {
	d.asked = append(d.asked, req.URL.Host)
	if req.URL.Scheme != "https" || req.URL.Path != "/v1/chain/faucet" || req.Method != http.MethodPost {
		return nil, fmt.Errorf("unexpected request %s %s", req.Method, req.URL)
	}
	raw, _ := io.ReadAll(req.Body)
	d.body = string(raw)
	a, ok := d.answers[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("no answer scripted for %s", req.URL.Host)
	}
	if a.err != nil {
		return nil, a.err
	}
	return &http.Response{StatusCode: a.status, Body: io.NopCloser(strings.NewReader(a.body))}, nil
}

func paid(amount int64) seedAnswer {
	return seedAnswer{status: http.StatusOK, body: fmt.Sprintf(`{"tx_hash":"%s","amount":"%d","height":"9"}`, strings.Repeat("AB", 32), amount)}
}

func refused(status int, kind, message string) seedAnswer {
	return seedAnswer{status: status, body: fmt.Sprintf(`{"error":%q,"message":%q}`, kind, message)}
}

var faucetNetwork = &netregistry.Manifest{Name: "stagenet", Seeds: []string{"seed1.stagenet.example", "seed2.stagenet.example", "seed3.stagenet.example"}}

func TestGatewayFaucet_theFirstSeedToPayEndsTheAsking(t *testing.T) {
	d := &seedDoer{answers: map[string]seedAnswer{"seed1.stagenet.example": paid(5)}}
	if err := (gatewayFaucet{client: d}).Fund(context.Background(), faucetNetwork, testOperator, big.NewInt(5)); err != nil {
		t.Fatal(err)
	}
	if len(d.asked) != 1 || d.asked[0] != "seed1.stagenet.example" {
		t.Errorf("asked %v, want only the first seed", d.asked)
	}
	if !strings.Contains(d.body, testOperator) || !strings.Contains(d.body, `"amount":"5"`) {
		t.Errorf("request body %s", d.body)
	}
}

// Seeds are replicas: one that serves no faucet, is busy or cannot be reached says nothing about the
// network, and the next is asked.
func TestGatewayFaucet_aSeedThatCannotPayIsPassedOver(t *testing.T) {
	d := &seedDoer{answers: map[string]seedAnswer{
		"seed1.stagenet.example": {status: http.StatusNotFound, body: "not found\n"},
		"seed2.stagenet.example": {err: errors.New("connection refused")},
		"seed3.stagenet.example": paid(7),
	}}
	if err := (gatewayFaucet{client: d}).Fund(context.Background(), faucetNetwork, testOperator, big.NewInt(7)); err != nil {
		t.Fatal(err)
	}
	if len(d.asked) != 3 {
		t.Errorf("asked %v, want all three in order", d.asked)
	}
	for _, kind := range []string{"busy", "unavailable", "faucet_disabled", "faucet_failed"} {
		d := &seedDoer{answers: map[string]seedAnswer{
			"seed1.stagenet.example": refused(http.StatusServiceUnavailable, kind, "this seed cannot"),
			"seed2.stagenet.example": paid(1),
		}}
		if err := (gatewayFaucet{client: d}).Fund(context.Background(), faucetNetwork, testOperator, big.NewInt(1)); err != nil || len(d.asked) != 2 {
			t.Errorf("%s: err %v, asked %v", kind, err, d.asked)
		}
	}
}

// What the chain refused, every seed would refuse: the run is told at once and no other seed is asked.
func TestGatewayFaucet_aRefusalOfTheChainEndsTheAsking(t *testing.T) {
	for kind, status := range map[string]int{"cooldown": 429, "epoch_cap": 503, "bad_amount": 400, "bad_recipient": 400} {
		t.Run(kind, func(t *testing.T) {
			d := &seedDoer{answers: map[string]seedAnswer{"seed1.stagenet.example": refused(status, kind, "the chain says no")}}
			err := (gatewayFaucet{client: d}).Fund(context.Background(), faucetNetwork, testOperator, big.NewInt(1))
			if err == nil || !strings.Contains(err.Error(), "the chain says no") || !strings.Contains(err.Error(), "seed1.stagenet.example") || !strings.Contains(err.Error(), kind) {
				t.Fatalf("err = %v", err)
			}
			if len(d.asked) != 1 {
				t.Errorf("asked %v after a refusal no other seed could change", d.asked)
			}
		})
	}
}

// A drip that is being made or was sent is not refused: no other seed is asked, since it could drip
// twice, and the balance poll that follows every drip says whether the account was paid.
func TestGatewayFaucet_aPendingDripEndsTheAskingWithoutAnError(t *testing.T) {
	d := &seedDoer{answers: map[string]seedAnswer{"seed1.stagenet.example": refused(504, "pending", "being made")}}
	if err := (gatewayFaucet{client: d}).Fund(context.Background(), faucetNetwork, testOperator, big.NewInt(1)); err != nil {
		t.Fatalf("a pending drip is an error: %v", err)
	}
	if len(d.asked) != 1 {
		t.Errorf("asked %v after a drip that is on its way", d.asked)
	}
}

func TestGatewayFaucet_whenNoSeedPaysEverySeedsAnswerIsReported(t *testing.T) {
	d := &seedDoer{answers: map[string]seedAnswer{
		"seed1.stagenet.example": {status: http.StatusNotFound, body: "not found"},
		"seed2.stagenet.example": refused(503, "unavailable", "the faucet account has no funds"),
		"seed3.stagenet.example": {err: errors.New("connection refused")},
	}}
	err := (gatewayFaucet{client: d}).Fund(context.Background(), faucetNetwork, testOperator, big.NewInt(1))
	if err == nil {
		t.Fatal("a network whose seeds all failed was funded")
	}
	for _, want := range []string{"seed1.stagenet.example: seed1.stagenet.example serves no faucet", "seed2.stagenet.example: unavailable: the faucet account has no funds", "seed3.stagenet.example: ask the faucet of seed3.stagenet.example", "connection refused", "none of the 3 seeds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("the report is not on one line: %q", err)
	}
}

func TestGatewayFaucet_noSeedsOrACancelledRunIsNotAnAnswer(t *testing.T) {
	d := &seedDoer{}
	if err := (gatewayFaucet{client: d}).Fund(context.Background(), &netregistry.Manifest{Name: "stagenet"}, testOperator, big.NewInt(1)); err == nil || len(d.asked) != 0 {
		t.Errorf("a network with no seeds: %v, asked %v", err, d.asked)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d = &seedDoer{answers: map[string]seedAnswer{"seed1.stagenet.example": {err: ctx.Err()}}}
	if err := (gatewayFaucet{client: d}).Fund(ctx, faucetNetwork, testOperator, big.NewInt(1)); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled run = %v, want the cancellation, not a report on every seed", err)
	}
	if len(d.asked) > 1 {
		t.Errorf("asked %v after the run was cancelled", d.asked)
	}
}

func TestNewGatewayFaucet_waitsLongEnoughForABlock(t *testing.T) {
	f := newGatewayFaucet()
	client, ok := f.client.(*http.Client)
	if !ok || client.Timeout < 45*time.Second {
		t.Fatalf("client %T with timeout %v: the gateway answers when the drip is in a block, up to 45 seconds", f.client, client.Timeout)
	}
}

func TestSSHFaucet(t *testing.T) {
	var gotEnv, gotAddr string
	var gotAmount *big.Int
	f := sshFaucet{
		environment: func(name string) (*cli.Environment, error) {
			return &cli.Environment{Name: name, Nodes: []cli.EnvNode{{Host: "203.0.113.1"}}}, nil
		},
		fund: func(env, recipient string, amount *big.Int) error {
			gotEnv, gotAddr, gotAmount = env, recipient, amount
			return nil
		},
	}
	m := &netregistry.Manifest{Name: "stagenet"}
	if err := f.Fund(context.Background(), m, testOperator, big.NewInt(5)); err != nil {
		t.Fatal(err)
	}
	if gotEnv != "stagenet" || gotAddr != testOperator || gotAmount.Int64() != 5 {
		t.Errorf("%q %q %v", gotEnv, gotAddr, gotAmount)
	}
}

func TestSSHFaucet_noAccessToANodeOfTheNetwork(t *testing.T) {
	f := sshFaucet{
		environment: func(string) (*cli.Environment, error) { return nil, errors.New("not found") },
		fund: func(string, string, *big.Int) error {
			t.Fatal("must not sign a drip with no node to sign on")
			return nil
		},
	}
	err := f.Fund(context.Background(), &netregistry.Manifest{Name: "stagenet"}, testOperator, big.NewInt(1))
	if err == nil || !strings.Contains(err.Error(), "no SSH access") {
		t.Fatalf("got %v", err)
	}
	empty := sshFaucet{
		environment: func(n string) (*cli.Environment, error) { return &cli.Environment{Name: n}, nil },
		fund:        func(string, string, *big.Int) error { t.Fatal("no nodes, no drip"); return nil },
	}
	if err := empty.Fund(context.Background(), &netregistry.Manifest{Name: "stagenet"}, testOperator, big.NewInt(1)); err == nil {
		t.Error("an environment with no nodes cannot sign")
	}
}

// ---- domain

// nsMachine is a cluster machine that answers the nameserver query as asked.
type nsMachine struct {
	*fakeMachine
	delegations []dnsdelegation.Delegation
	err         error
}

func (m nsMachine) Nameservers(context.Context) ([]dnsdelegation.Delegation, error) {
	return m.delegations, m.err
}

func clusterWith(ds ...dnsdelegation.Delegation) nsMachine {
	return nsMachine{fakeMachine: &fakeMachine{w: newWorld(), ip: ip1}, delegations: ds}
}

func delegation() dnsdelegation.Delegation {
	return dnsdelegation.Delegation{Domain: "cluster.example.org", Nameservers: []dnsdelegation.Nameserver{{Hostname: "ns1", IP: "203.0.113.10"}}}
}

func TestClusterDomain_recordsAreTheClustersNSAndGlue(t *testing.T) {
	c := clusterDomain{}
	records, err := c.Records(context.Background(), clusterWith(delegation()), "cluster.example.org")
	if err != nil || len(records) != 2 || !strings.Contains(records[0], "NS\tns1.cluster.example.org.") || !strings.Contains(records[1], "A\t203.0.113.10") {
		t.Fatalf("%v, %v", records, err)
	}
	if _, err := c.Records(context.Background(), clusterWith(delegation()), "other.example.org"); err == nil {
		t.Error("a domain the cluster does not serve has no records")
	}
}

func leafState(t *testing.T, subject, issuer pkix.Name, dns []string, notAfter time.Time) *tls.ConnectionState {
	t.Helper()
	return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{
		Subject: subject, Issuer: issuer, DNSNames: dns, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
	}}}
}

func TestCertServes(t *testing.T) {
	now := time.Now()
	ca, leaf := pkix.Name{CommonName: "Let's Encrypt R10"}, pkix.Name{CommonName: "cluster.example.org"}
	if !certServes(leafState(t, leaf, ca, []string{"cluster.example.org"}, now.Add(time.Hour)), "cluster.example.org", now) {
		t.Error("an issued certificate for the name serves")
	}
	if !certServes(leafState(t, leaf, ca, []string{"*.example.org"}, now.Add(time.Hour)), "cluster.example.org", now) {
		t.Error("a wildcard of the parent serves")
	}
	if certServes(leafState(t, leaf, leaf, []string{"cluster.example.org"}, now.Add(time.Hour)), "cluster.example.org", now) {
		t.Error("the self-signed fallback is not an issued certificate")
	}
	if certServes(leafState(t, leaf, ca, []string{"other.example.org"}, now.Add(time.Hour)), "cluster.example.org", now) {
		t.Error("a certificate for another name")
	}
	if certServes(leafState(t, leaf, ca, []string{"cluster.example.org"}, now.Add(-time.Minute)), "cluster.example.org", now) {
		t.Error("an expired certificate")
	}
	if certServes(nil, "x", now) || certServes(&tls.ConnectionState{}, "x", now) {
		t.Error("no certificate, no service")
	}
}

func TestClusterDomain_waitEndsWhenDelegatedAndCertified(t *testing.T) {
	findings := []dnsdelegation.Finding{{Kind: dnsdelegation.FindingMissingNS}}
	calls := 0
	c := clusterDomain{
		check: func(context.Context, dnsdelegation.Delegation) ([]dnsdelegation.Finding, error) {
			calls++
			if calls < 3 {
				return findings, nil
			}
			return nil, nil
		},
		dialCert: func(context.Context, string) (*tls.ConnectionState, error) {
			return leafState(t, pkix.Name{CommonName: "cluster.example.org"}, pkix.Name{CommonName: "CA"}, []string{"cluster.example.org"}, time.Now().Add(time.Hour)), nil
		},
	}
	if err := c.Wait(context.Background(), clusterWith(delegation()), "cluster.example.org", time.Millisecond, time.Second); err != nil || calls != 3 {
		t.Fatalf("%d checks, %v", calls, err)
	}
}

func TestClusterDomain_waitGivesUpWithWhatItWaitedFor(t *testing.T) {
	c := clusterDomain{
		check: func(context.Context, dnsdelegation.Delegation) ([]dnsdelegation.Finding, error) {
			return []dnsdelegation.Finding{{Kind: dnsdelegation.FindingMissingGlue}}, nil
		},
	}
	err := c.Wait(context.Background(), clusterWith(delegation()), "cluster.example.org", time.Millisecond, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "cluster.example.org to be delegated") {
		t.Fatalf("got %v", err)
	}
}

func TestDialCertificate_readsWhatTheServerPresents(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	state, err := dialCertificate(context.Background(), "cluster.example.org", strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	if len(state.PeerCertificates) == 0 {
		t.Fatal("the dial returned no certificate")
	}
	// The test server's certificate is not for this name, which certServes says.
	if certServes(state, "cluster.example.org", time.Now()) {
		t.Error("a certificate for another name must not count")
	}
	if _, err := dialCertificate(context.Background(), "cluster.example.org", "127.0.0.1:1"); err == nil {
		t.Error("a closed port is an error for the caller to poll again")
	}
}

// ---- recorder (against the CLI's own config)

func TestCLIRecorder_createsTheEnvironmentAndKeepsAnExistingGateway(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := cliRecorder{}
	if err := r.Cluster("stagenet-alice", "stagenet", "https://203.0.113.11", []RecordedNode{{Host: "203.0.113.11", User: "root", Role: "node"}}); err != nil {
		t.Fatal(err)
	}
	env, err := cli.GetEnvironmentByName("stagenet-alice")
	if err != nil || env.Network != "stagenet" || env.GatewayURL != "https://203.0.113.11" || len(env.Nodes) != 1 {
		t.Fatalf("%+v, %v", env, err)
	}
	// A run that adds a node to an existing environment passes no gateway.
	if err := r.Cluster("stagenet-alice", "stagenet", "", []RecordedNode{{Host: "203.0.113.12", User: "root", Role: "node"}}); err != nil {
		t.Fatal(err)
	}
	env, _ = cli.GetEnvironmentByName("stagenet-alice")
	if env.GatewayURL != "https://203.0.113.11" || len(env.Nodes) != 2 {
		t.Errorf("%+v", env)
	}
	if got := r.Hosts("stagenet-alice"); len(got) != 2 || got[1].Host != "203.0.113.12" {
		t.Errorf("hosts %v", got)
	}
	if got := r.Hosts("nope"); len(got) != 0 {
		t.Errorf("hosts of an unknown environment: %v", got)
	}
	if err := r.Cluster("fresh", "stagenet", "", nil); err == nil {
		t.Error("a new environment needs a gateway")
	}
	if err := r.Operator("stagenet-alice", testOperator); err != nil {
		t.Fatal(err)
	}
	if env, _ := cli.GetEnvironmentByName("stagenet-alice"); env.Operator != testOperator {
		t.Errorf("operator %q", env.Operator)
	}
}

func TestCLIRecorder_activeFor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := cliRecorder{}
	if got := r.ActiveFor("stagenet"); got != "" {
		t.Errorf("no environment, no active one: %q", got)
	}
	if err := cli.AddEnvironmentOn("mine", "https://203.0.113.11", "d", "stagenet"); err != nil {
		t.Fatal(err)
	}
	if err := cli.SwitchEnvironment("mine"); err != nil {
		t.Fatal(err)
	}
	if got := r.ActiveFor("stagenet"); got != "mine" {
		t.Errorf("got %q", got)
	}
	if got := r.ActiveFor("testnet"); got != "" {
		t.Errorf("an environment on another network is not this network's: %q", got)
	}
}

func TestClusterDomain_findingsCannotDriveTheTerminal(t *testing.T) {
	c := clusterDomain{
		check: func(context.Context, dnsdelegation.Delegation) ([]dnsdelegation.Finding, error) {
			return []dnsdelegation.Finding{{Kind: dnsdelegation.FindingMissingNS, Record: "x\x1b[2J.example.org", Want: "ns1"}}, nil
		},
	}
	_, err := c.ready(context.Background(), clusterWith(delegation()), "cluster.example.org")
	if err == nil || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("got %q: what the parent zone answered is text someone else wrote", err)
	}
}

func TestCliChecksum(t *testing.T) {
	good := `{"version":"0.3.1","checksums":{"orama":"` + strings.ToUpper(testCLISHA) + `","oramad":"` + testManifest + `"}}`
	if got, err := cliChecksum([]byte(good)); err != nil || got != testCLISHA {
		t.Fatalf("got %q, %v: the digest is compared as lowercase hex", got, err)
	}
	for name, doc := range map[string]string{
		"not json":     "<html>",
		"no checksums": `{"version":"1"}`,
		"no orama":     `{"checksums":{"oramad":"` + testManifest + `"}}`,
		"not a digest": `{"checksums":{"orama":"abc"}}`,
	} {
		if _, err := cliChecksum([]byte(doc)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestClusterDomain_theFinalErrorSaysWhichQuestionWasUnanswered(t *testing.T) {
	cases := map[string]struct {
		via  Machine
		c    clusterDomain
		want string
	}{
		"the cluster has no nameserver": {
			nsMachine{fakeMachine: &fakeMachine{w: newWorld(), ip: ip1}, err: errors.New("ssh: connection refused")},
			clusterDomain{},
			"the cluster's nameservers: ssh: connection refused",
		},
		"DNS cannot be asked": {
			clusterWith(delegation()),
			clusterDomain{
				check: func(context.Context, dnsdelegation.Delegation) ([]dnsdelegation.Finding, error) {
					return nil, errors.New("SERVFAIL")
				},
			},
			"asking DNS: SERVFAIL",
		},
		"the records are missing": {
			clusterWith(delegation()),
			clusterDomain{
				check: func(context.Context, dnsdelegation.Delegation) ([]dnsdelegation.Finding, error) {
					return []dnsdelegation.Finding{{Kind: dnsdelegation.FindingMissingNS, Record: "cluster.example.org", Want: "ns1.cluster.example.org"}}, nil
				},
			},
			"the parent zone does not return the records yet",
		},
		"no certificate yet": {
			clusterWith(delegation()),
			clusterDomain{
				check: func(context.Context, dnsdelegation.Delegation) ([]dnsdelegation.Finding, error) { return nil, nil },
				dialCert: func(context.Context, string) (*tls.ConnectionState, error) {
					return nil, errors.New("connection refused")
				},
			},
			"the cluster's certificate: connection refused",
		},
	}
	for name, tc := range cases {
		err := tc.c.Wait(context.Background(), tc.via, "cluster.example.org", time.Millisecond, 15*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want it to contain %q", name, err, tc.want)
		}
	}
}
