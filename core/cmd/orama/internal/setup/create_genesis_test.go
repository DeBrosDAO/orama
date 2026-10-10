package setup

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	seatAddrA = "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"
	seatAddrB = "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	seatKeyA  = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	seatKeyB  = "ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8="
)

func twoSeats() []Seat {
	return []Seat{
		{Moniker: "seed", Address: seatAddrA, ConsensusPubKey: seatKeyA, NodeID: strings.Repeat("a", 40)},
		{Moniker: "seed-2", Address: seatAddrB, ConsensusPubKey: seatKeyB, NodeID: strings.Repeat("b", 40)},
	}
}

func TestGenesisSteps_testNetworkWithFaucet(t *testing.T) {
	got := GenesisSteps(GenesisSpec{ChainID: "orama-stagenet-6", Seats: twoSeats(), TestNetwork: true, Faucet: true})
	want := [][]string{
		{"init", "genesis-work", "--chain-id", "orama-stagenet-6", "--default-denom", "norama"},
		{"genesis", "set-emission-params", "--epoch-duration", "300s", "--min-blocks-per-epoch", "10", "--allow-bootstrap-stake", "--faucet-enabled", "--faucet-max-drip", "10000000000000"},
		{"genesis", "add-bootstrap-validator", seatAddrA, "--moniker", "seed", "--consensus-pubkey-base64", seatKeyA, "--min-committee-size", "2"},
		{"genesis", "add-bootstrap-validator", seatAddrB, "--moniker", "seed-2", "--consensus-pubkey-base64", seatKeyB},
		{"genesis", "add-standard-contracts"},
		{"genesis", "validate"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("genesis steps:\n got %q\nwant %q", got, want)
	}
}

func TestGenesisSteps_noFaucet(t *testing.T) {
	got := GenesisSteps(GenesisSpec{ChainID: "orama-devnet-1", Seats: twoSeats()[:1], TestNetwork: true})
	if flag := strings.Join(got[1], " "); strings.Contains(flag, "--faucet-enabled") || !strings.Contains(flag, "--allow-bootstrap-stake") {
		t.Errorf("emission step = %q", flag)
	}
}

func TestGenesisSteps_productionKeepsTheChainsEpochs(t *testing.T) {
	got := GenesisSteps(GenesisSpec{ChainID: "orama-1", Seats: twoSeats()})
	for _, step := range got {
		if len(step) > 1 && step[1] == "set-emission-params" {
			t.Fatalf("a production genesis must not set the test epochs or allow a supply: %q", step)
		}
	}
	if len(got) != 1+2+2 {
		t.Errorf("production steps = %d, want init, two seats, contracts and validate", len(got))
	}
}

func TestGenesisScript_quotesAndCleansUp(t *testing.T) {
	script := genesisScript([][]string{{"init", "genesis-work", "--chain-id", "x'; rm -rf /; '"}, {"genesis", "validate"}})
	for _, want := range []string{
		"set -eu", "trap 'rm -rf \"$work\"' EXIT", "install -d -m 0700 -o orama-chain -g orama-chain \"$work\"",
		`runuser -u orama-chain -- /usr/lib/orama-global/bin/oramad --home "$work" init genesis-work --chain-id 'x'"'"'; rm -rf /; '"'"'' >/dev/null`,
		`--home "$work" genesis validate >/dev/null`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if !strings.HasSuffix(script, "cat \"$work/config/genesis.json\"\n") {
		t.Errorf("the genesis must be the last thing printed:\n%s", script)
	}
}

const sampleGenesis = `{"genesis_time":"2026-10-10T00:00:00Z","chain_id":"orama-stagenet-6",
 "consensus":{"params":{"block":{"max_bytes":"22020096","max_gas":"-1"},"evidence":{"max_age_num_blocks":"100000"}}},
 "app_state":{"big":12345678901234567890123,"note":"a & b <c>"}}`

func decodeParams(t *testing.T, genesis []byte) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Consensus struct {
			Params map[string]map[string]any `json:"params"`
		} `json:"consensus"`
	}
	if err := json.Unmarshal(genesis, &doc); err != nil {
		t.Fatalf("the result is not JSON: %v\n%s", err, genesis)
	}
	return doc.Consensus.Params
}

func TestApplyConsensusParams_setsTheTwoValuesAndKeepsTheRest(t *testing.T) {
	out, err := ApplyConsensusParams([]byte(sampleGenesis))
	if err != nil {
		t.Fatal(err)
	}
	params := decodeParams(t, out)
	if params["block"]["max_gas"] != "100000000" || params["block"]["max_bytes"] != "22020096" {
		t.Errorf("block = %v", params["block"])
	}
	if params["abci"]["vote_extensions_enable_height"] != "2" {
		t.Errorf("abci = %v", params["abci"])
	}
	if params["evidence"]["max_age_num_blocks"] != "100000" {
		t.Errorf("evidence = %v", params["evidence"])
	}
	for _, kept := range []string{`12345678901234567890123`, `"a & b <c>"`, `"chain_id": "orama-stagenet-6"`} {
		if !strings.Contains(string(out), kept) {
			t.Errorf("the edit lost %s:\n%s", kept, out)
		}
	}
}

func TestApplyConsensusParams_idempotent(t *testing.T) {
	once, err := ApplyConsensusParams([]byte(sampleGenesis))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := ApplyConsensusParams(once)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("applying it twice changed the genesis:\n%s\n---\n%s", once, twice)
	}
}

func TestApplyConsensusParams_missingConsensus(t *testing.T) {
	out, err := ApplyConsensusParams([]byte(`{"chain_id":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	params := decodeParams(t, out)
	if params["block"]["max_gas"] != "100000000" || params["abci"]["vote_extensions_enable_height"] != "2" {
		t.Errorf("params = %v", params)
	}
}

func TestApplyConsensusParams_notAnObject(t *testing.T) {
	for _, in := range []string{``, `[]`, `{"consensus":[]}`, `{"consensus":{"params":"x"}}`} {
		if _, err := ApplyConsensusParams([]byte(in)); err == nil {
			t.Errorf("ApplyConsensusParams(%q) did not fail", in)
		}
	}
}
