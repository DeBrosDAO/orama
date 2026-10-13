package setup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/globalbind"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

// relayHarness is a harness on a network that pins a Tor network, so the nodes run a relay.
func relayHarness() *harness {
	h := newHarness()
	h.deps.Networks = fakeNetworks{w: h.w, torNetwork: []byte(`{"name":"pinned"}`)}
	return h
}

func fakeRelayHex(ip string) string { return fmt.Sprintf("%X", fakeRelayFingerprint(ip)) }

func TestRun_aRelayNodeRegistersItsKeyWithTheNodeAndItsFingerprintAfterTheBond(t *testing.T) {
	h := relayHarness()
	mustRun(t, h, h.opts(ip1))
	log := strings.Join(h.w.entries(), "\n")

	if h.w.index("tx register-node alice roles=[2 3] asn=24940 bindings=hot-key,consensus,relay") < 0 {
		t.Errorf("the node registers with the relay's key bound as service \"relay\":\n%s", log)
	}
	want := fmt.Sprintf("tx register-relay alice fp=%s exit=false", fakeRelayHex(ip1))
	register, bond := h.w.index(want), h.w.index("tx bond alice role=3")
	if register < 0 || bond < 0 || register < bond {
		t.Errorf("the relay registers once, after the node and its bond (register %d, bond %d):\n%s", register, bond, log)
	}
	if h.w.index("relay-identity "+ip1+" node=alice") < 0 {
		t.Errorf("the relay signed on the node:\n%s", log)
	}
	if got := h.w.relays["alice"]; got == nil || !bytes.Equal(got.Fingerprint, fakeRelayFingerprint(ip1)) {
		t.Errorf("the chain holds %+v", got)
	}
}

func TestRun_anExitRelayIsRegisteredAsAnExit(t *testing.T) {
	h := relayHarness()
	opts := h.opts(ip1)
	opts.Exit = true
	mustRun(t, h, opts)
	if h.w.index(fmt.Sprintf("tx register-relay alice fp=%s exit=true", fakeRelayHex(ip1))) < 0 {
		t.Errorf("the exit declaration is part of the registration:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aRelayAlreadyRegisteredIsNotRegisteredAgain(t *testing.T) {
	h := relayHarness()
	mustRun(t, h, h.opts(ip1))
	mustRun(t, h, h.opts(ip1))
	if h.w.count("tx register-relay") != 1 || h.w.count("tx update-relay") != 0 || h.w.count("tx update-bindings") != 0 {
		t.Errorf("a re-run sent a relay transaction again:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aNodeRegisteredWithoutTheRelayKeyGetsItAndItsRelay(t *testing.T) {
	h := relayHarness()
	h.w.operatorRegistered = true
	h.w.nodes["alice"] = &RegisteredNode{Roles: []int{clusterreg.RoleStorage, clusterreg.RoleRelay}, Bonds: map[int]*big.Int{}, Bindings: hotOnly()}
	mustRun(t, h, h.opts(ip1))

	if h.w.count("tx register-node") != 0 {
		t.Fatalf("the registered node is updated, not registered again:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if got := serviceList(h.w.nodes["alice"].Bindings); got != "hot-key,consensus,relay" {
		t.Errorf("the updates keep what the set held and add the two keys: %s", got)
	}
	if h.w.count("tx register-relay alice") != 1 {
		t.Errorf("the relay is registered once:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aRelayWhoseFingerprintChangedIsUpdatedNotRegisteredAgain(t *testing.T) {
	h := relayHarness()
	mustRun(t, h, h.opts(ip1))
	h.w.relays["alice"].Fingerprint = bytes.Repeat([]byte{0xee}, clusterreg.RelayFingerprintLen)
	mustRun(t, h, h.opts(ip1))
	if h.w.count("tx register-relay") != 1 || h.w.index(fmt.Sprintf("tx update-relay alice fp=%s", fakeRelayHex(ip1))) < 0 {
		t.Errorf("a rotated fingerprint is an update:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aRelayHeldWithAnotherKeyOrExitDeclarationIsUpdated(t *testing.T) {
	for name, change := range map[string]func(*RegisteredRelay){
		"another ed25519 key":      func(r *RegisteredRelay) { r.Ed25519ID = bytes.Repeat([]byte{1}, 32) },
		"another exit declaration": func(r *RegisteredRelay) { r.Exit = !r.Exit },
	} {
		t.Run(name, func(t *testing.T) {
			h := relayHarness()
			mustRun(t, h, h.opts(ip1))
			change(h.w.relays["alice"])
			mustRun(t, h, h.opts(ip1))
			if h.w.count("tx update-relay alice") != 1 {
				t.Errorf("the relay is updated once:\n%s", strings.Join(h.w.entries(), "\n"))
			}
		})
	}
}

func TestRun_aRelayIdentityTheChainWouldRefuseStopsTheRunBeforeAnyTransaction(t *testing.T) {
	tamper := map[string]func(*NodeIdentity){
		"no identity at all": func(id *NodeIdentity) { id.RelayIdentity = nil },
		"a cross-certificate of another node": func(id *NodeIdentity) {
			other, _ := globalbind.NewRelayIdentity(fakeRelaySecret(ip1), fakeRelayRSA(ip1), testChainID, testOperator, "someone-else", false)
			id.RelayIdentity, _ = json.Marshal(other)
		},
		"a binding for another operator": func(id *NodeIdentity) {
			other, _ := globalbind.NewRelayIdentity(fakeRelaySecret(ip1), fakeRelayRSA(ip1), testChainID, "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s", "alice", false)
			id.RelayIdentity, _ = json.Marshal(other)
		},
		"text": func(id *NodeIdentity) { id.RelayIdentity = []byte("no relay here") },
	}
	for name, edit := range tamper {
		t.Run(name, func(t *testing.T) {
			h := relayHarness()
			h.enroll.tamperIdentity = edit
			_, err := run(t, h, h.opts(ip1))
			if err == nil || !strings.Contains(err.Error(), "relay") {
				t.Fatalf("got %v, want the relay identity refused", err)
			}
			if h.w.count("tx register-node") != 0 || h.w.count("tx register-relay") != 0 {
				t.Error("a transaction was sent on an identity the chain would refuse")
			}
		})
	}
}

func TestRun_aNodeWithoutARelayAsksForNoRelayIdentity(t *testing.T) {
	h := newHarness()
	mustRun(t, h, h.opts(ip1))
	if h.w.count("relay-identity") != 0 || h.w.count("tx register-relay") != 0 || h.w.index("tx register-node alice roles=[2] asn=24940 bindings=hot-key,consensus") < 0 || strings.Contains(strings.Join(h.w.entries(), "\n"), "consensus,relay") {
		t.Errorf("a node that runs no relay registers none:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aRefusedRelayRegistrationIsAnErrorNamingTheFingerprint(t *testing.T) {
	h := relayHarness()
	h.w.operatorRegistered = true
	h.w.nodes["alice"] = &RegisteredNode{Roles: []int{clusterreg.RoleStorage, clusterreg.RoleRelay}, Bonds: map[int]*big.Int{}, Bindings: hotOnly()}
	// The node is held without the "relay" binding by the fake's chain, so the registration is refused.
	h.deps.Chain = &refusingRelays{fakeChain: h.chain}
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), fakeRelayHex(ip1)) {
		t.Fatalf("got %v, want the refused relay's fingerprint", err)
	}
}

// refusingRelays is a chain whose relay registrations the chain refuses.
type refusingRelays struct{ *fakeChain }

func (c *refusingRelays) Open(ctx context.Context, m Machine, id string) (ChainSession, error) {
	s, err := c.fakeChain.Open(ctx, m, id)
	if err != nil {
		return nil, err
	}
	return refusingSession{s}, nil
}

type refusingSession struct{ ChainSession }

func (refusingSession) RegisterRelay(context.Context, clusterreg.RelayRegistration) (*onchain.Receipt, error) {
	return nil, fmt.Errorf("fingerprint already registered")
}

func TestIdentityScript_theRelayKeySignsOnTheNodeAndOnlyWhenAsked(t *testing.T) {
	script := identityScript(IdentityRequest{ChainID: testChainID, Operator: testOperator, RelayNodeID: "alice"})
	for _, want := range []string{"__RELAY_IDENTITY__", "global tor relay-identity --chain-id", "--node-id 'alice'"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "--exit") {
		t.Errorf("a relay that is not an exit signs without --exit:\n%s", script)
	}
	exit := identityScript(IdentityRequest{ChainID: testChainID, Operator: testOperator, RelayNodeID: "alice", RelayExit: true})
	if !strings.Contains(exit, "--node-id 'alice' --exit\n") {
		t.Errorf("an exit relay signs for the exit declaration:\n%s", exit)
	}
	if plain := identityScript(IdentityRequest{ChainID: testChainID, Operator: testOperator}); strings.Contains(plain, "relay-identity") {
		t.Errorf("a node that was not asked prints no relay identity:\n%s", plain)
	}
	if strings.Contains(script, "ed25519_master_id_secret_key") || strings.Contains(script, "cat ") {
		t.Errorf("the script reads a key itself instead of asking the command to sign:\n%s", script)
	}
}

func TestParseIdentity_theRelayIdentityIsKeptWhenTheNodePrintsIt(t *testing.T) {
	base := identityOutput(t, strings.Repeat("ab", 20), make([]byte, 32), nil)
	id, err := ParseIdentity(base + "__RELAY_IDENTITY__\n{\"rsa_fingerprint\": \"AB\"}\n")
	if err != nil || !strings.Contains(string(id.RelayIdentity), "rsa_fingerprint") {
		t.Fatalf("%+v, %v", id, err)
	}
	if plain, err := ParseIdentity(base); err != nil || plain.RelayIdentity != nil {
		t.Errorf("a node that was not asked prints none: %+v, %v", plain.RelayIdentity, err)
	}
}

// relayQuery serves x/relay's OperatorRelays in pages of one, as the REST gateway answers it.
func relayQuery(t *testing.T, pages [][]string, seen *[]string) *restSession {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.String())
		index := 0
		if key := r.URL.Query().Get("pagination.key"); key != "" {
			fmt.Sscanf(key, "page-%d", &index)
		}
		entries := make([]map[string]any, 0, len(pages[index]))
		for _, node := range pages[index] {
			entries = append(entries, map[string]any{"relay": map[string]any{
				"node_id":         node,
				"rsa_fingerprint": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 20)),
				"ed25519_id":      base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32)),
				"exit":            true,
			}})
		}
		next := ""
		if index+1 < len(pages) {
			next = fmt.Sprintf("page-%d", index+1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"relays": entries, "pagination": map[string]string{"next_key": next}})
	}))
	t.Cleanup(srv.Close)
	return &restSession{base: srv.URL, http: srv.Client(), stop: func() {}}
}

func TestRelayOfNode_findsTheRelayOnALaterPage(t *testing.T) {
	var seen []string
	s := relayQuery(t, [][]string{{"node-a"}, {"node-b"}}, &seen)
	got, err := s.RelayOfNode(context.Background(), testOperator, "node-b")
	if err != nil || got == nil {
		t.Fatalf("%+v, %v", got, err)
	}
	if !bytes.Equal(got.Fingerprint, bytes.Repeat([]byte{0x11}, 20)) || !bytes.Equal(got.Ed25519ID, bytes.Repeat([]byte{0x22}, 32)) || !got.Exit {
		t.Errorf("%+v", got)
	}
	if len(seen) != 2 || !strings.Contains(seen[0], "/orama/relay/v1/operator-relays/"+testOperator) || !strings.Contains(seen[1], "pagination.key=page-1") {
		t.Errorf("requests %v", seen)
	}
}

func TestRelayOfNode_aNodeWithNoRelayIsNilNotAnError(t *testing.T) {
	var seen []string
	s := relayQuery(t, [][]string{{"node-a"}}, &seen)
	if got, err := s.RelayOfNode(context.Background(), testOperator, "node-z"); err != nil || got != nil {
		t.Fatalf("%+v, %v", got, err)
	}
	if _, err := s.RelayOfNode(context.Background(), "not-an-address", "node-a"); err == nil {
		t.Error("a bad operator was accepted")
	}
}
