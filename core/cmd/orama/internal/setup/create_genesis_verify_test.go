package setup

import (
	"encoding/json"
	"strings"
	"testing"
)

func verifySpec(n int) GenesisSpec {
	return GenesisSpec{ChainID: "orama-stagenet-6", Seats: fakeSeats(n), TestNetwork: true, Faucet: true}
}

func TestVerifyGenesis_theCommitteeAndNothingElse(t *testing.T) {
	if err := VerifyGenesis(finalGenesis(0, 1, 2), verifySpec(3)); err != nil {
		t.Fatalf("a genesis of exactly this committee was refused: %v", err)
	}
}

func TestVerifyGenesis_refusesWhatIsNotTheCommittee(t *testing.T) {
	seats := fakeSeats(3)
	good := genesisFor{chainID: "orama-stagenet-6", seats: seats, allowStake: true, faucet: true}
	wrongKey := append([]Seat(nil), seats...)
	wrongKey[1].ConsensusPubKey = fakeSeat(9).ConsensusPubKey
	wrongName := append([]Seat(nil), seats...)
	wrongName[2].Moniker = "other"
	cases := map[string]struct {
		g    genesisFor
		want string
	}{
		"another chain":             {genesisFor{chainID: "orama-stagenet-5", seats: seats, allowStake: true, faucet: true}, "not \"orama-stagenet-6\""},
		"a seat is missing":         {genesisFor{chainID: good.chainID, seats: seats[:2], allowStake: true, faucet: true}, "seed-3"},
		"a validator is extra":      {genesisFor{chainID: good.chainID, seats: append(append([]Seat(nil), seats...), fakeSeat(7)), allowStake: true, faucet: true}, "names 4 bootstrap validators"},
		"a seat twice":              {genesisFor{chainID: good.chainID, seats: []Seat{seats[0], seats[1], seats[1]}, allowStake: true, faucet: true}, "2 times"},
		"another consensus key":     {genesisFor{chainID: good.chainID, seats: wrongKey, allowStake: true, faucet: true}, "seed-2"},
		"another moniker":           {genesisFor{chainID: good.chainID, seats: wrongName, allowStake: true, faucet: true}, "seed-3"},
		"a faucet nobody asked for": {genesisFor{chainID: good.chainID, seats: seats, allowStake: true, faucet: false}, "faucet_enabled=false"},
		"no bootstrap stake":        {genesisFor{chainID: good.chainID, seats: seats, allowStake: false, faucet: true}, "allow_bootstrap_stake=false"},
		"a balance":                 {genesisFor{chainID: good.chainID, seats: seats, allowStake: true, faucet: true, extra: []string{"orama1thief"}}, "zero supply"},
	}
	for name, c := range cases {
		doc, err := ApplyConsensusParams(c.g.json())
		if err != nil {
			t.Fatal(err)
		}
		err = VerifyGenesis(doc, verifySpec(3))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want a refusal naming %q", name, err, c.want)
		}
	}
}

func TestVerifyGenesis_refusesWhatTheClassForbids(t *testing.T) {
	prod := GenesisSpec{ChainID: "orama-1", Seats: fakeSeats(2)}
	doc, err := ApplyConsensusParams(genesisFor{chainID: "orama-1", seats: fakeSeats(2), allowStake: true, faucet: true}.json())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyGenesis(doc, prod); err == nil || !strings.Contains(err.Error(), "allow_bootstrap_stake=true") {
		t.Errorf("a production genesis with a faucet and bootstrap stake was accepted: %v", err)
	}
}

func TestVerifyGenesis_theConsensusParametersMustBeSet(t *testing.T) {
	raw := genesisFor{chainID: "orama-stagenet-6", seats: fakeSeats(2), allowStake: true, faucet: true}.json()
	err := VerifyGenesis(raw, verifySpec(2))
	if err == nil || !strings.Contains(err.Error(), "max_gas") {
		t.Errorf("a genesis oramad wrote, before the consensus edit, was accepted: %v", err)
	}
}

func TestVerifyGenesis_aPlaceholderAndGarbageAreRefused(t *testing.T) {
	for name, in := range map[string]string{"placeholder": `{"chain_id":"orama-stagenet-6"}`, "garbage": `not json`, "empty": ``} {
		if err := VerifyGenesis([]byte(in), verifySpec(1)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestVerifyGenesis_theRefusalSaysHowToGoOn(t *testing.T) {
	err := VerifyGenesis(finalGenesis(0), verifySpec(2))
	if err == nil || !strings.Contains(err.Error(), "--force-new-genesis") || !strings.Contains(err.Error(), "orama setup --network") {
		t.Errorf("the refusal must name both ways out: %v", err)
	}
}

func TestCheckSeatsDistinct(t *testing.T) {
	seats := fakeSeats(3)
	if err := checkSeatsDistinct(seats); err != nil {
		t.Fatalf("three different seats were refused: %v", err)
	}
	for name, mutate := range map[string]func(*Seat){
		"node id":       func(s *Seat) { s.NodeID = seats[0].NodeID },
		"consensus key": func(s *Seat) { s.ConsensusPubKey = seats[0].ConsensusPubKey },
		"seat account":  func(s *Seat) { s.Address = seats[0].Address },
	} {
		dup := append([]Seat(nil), seats...)
		mutate(&dup[2])
		if err := checkSeatsDistinct(dup); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("two machines with the same %s: %v", name, err)
		}
	}
	if err := checkSeatsDistinct(nil); err != nil {
		t.Errorf("no seats: %v", err)
	}
}

// genesisWith is a valid genesis of two seats with the edit applied to its document.
func genesisWith(t *testing.T, edit func(doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(finalGenesis(0, 1), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func appState(doc map[string]any) map[string]any { return doc["app_state"].(map[string]any) }

func TestVerifyGenesis_refusesWhatTheOtherModulesCarry(t *testing.T) {
	cases := map[string]struct {
		edit func(map[string]any)
		want string
	}{
		"a staking validator": {func(d map[string]any) { appState(d)["staking"] = map[string]any{"validators": []string{"v"}} }, "staking.validators has 1 entries"},
		"an earnings account": {func(d map[string]any) { appState(d)["fees"] = map[string]any{"earnings_accounts": []string{"a"}} }, "fees.earnings_accounts"},
		"an operator":         {func(d map[string]any) { appState(d)["nodes"] = map[string]any{"operators": []string{"o"}} }, "nodes.operators"},
		"a wasm contract":     {func(d map[string]any) { appState(d)["wasm"] = map[string]any{"contracts": []string{"c"}} }, "wasm.contracts"},
		"an exported power state": {func(d map[string]any) {
			p := appState(d)["power"].(map[string]any)
			p["exported"] = true
			p["power_records"] = []string{"r"}
		}, "exported"},
		"a lambda above zero": {func(d map[string]any) { appState(d)["power"].(map[string]any)["lambda"] = "0.5" }, "lambda"},
	}
	for name, c := range cases {
		err := VerifyGenesis(genesisWith(t, c.edit), verifySpec(2))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want a refusal naming %q", name, err, c.want)
		}
	}
}

func TestVerifyGenesis_theStandardContractsAndTheMetadataAreFine(t *testing.T) {
	doc := genesisWith(t, func(d map[string]any) {
		appState(d)["wasm"] = map[string]any{"codes": []string{"1", "2", "3", "4", "5"}, "sequences": []string{"s"}, "contracts": []string{}}
		appState(d)["wasmpolicy"] = map[string]any{"genesis_code_ids": []int{1, 2, 3, 4, 5}}
		appState(d)["bank"].(map[string]any)["denom_metadata"] = []string{"m"}
	})
	if err := VerifyGenesis(doc, verifySpec(2)); err != nil {
		t.Errorf("the lists the genesis commands fill were refused: %v", err)
	}
}

// The chain reads protobuf JSON, which also takes a field's camelCase name; the CLI
// reads Go JSON. A genesis that spells a field both ways shows each a different list.
func TestVerifyGenesis_aFieldNamedTwiceIsRefused(t *testing.T) {
	good := string(finalGenesis(0, 1))
	for name, doc := range map[string]string{
		"a camelCase twin of the committee": strings.Replace(good, `"bootstrap_committee"`, `"bootstrap_committee":[],"bootstrapCommittee"`, 1),
		"a key twice":                       strings.Replace(good, `"chain_id"`, `"chain_id":"x","chain_id"`, 1),
		"a module in two cases":             strings.Replace(good, `"power"`, `"Power":{},"power"`, 1),
	} {
		err := VerifyGenesis([]byte(doc), verifySpec(2))
		if err == nil || !(strings.Contains(err.Error(), "name the same field") || strings.Contains(err.Error(), "there twice")) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if err := VerifyGenesis([]byte(good+` {}`), verifySpec(2)); err == nil {
		t.Error("a second document after the genesis was accepted")
	}
}

func TestSameGenesis_onlyTheTimeMayDiffer(t *testing.T) {
	a := genesisWith(t, func(d map[string]any) { d["genesis_time"] = "2026-10-10T10:00:00Z" })
	b := genesisWith(t, func(d map[string]any) { d["genesis_time"] = "2026-10-10T10:00:07Z" })
	if err := sameGenesis(a, b); err != nil {
		t.Errorf("two builds that differ in their time were refused: %v", err)
	}
	c := genesisWith(t, func(d map[string]any) {
		appState(d)["houses"] = map[string]any{"enacted": map[string]any{"scheduled_upgrade": map[string]any{"height": "1"}}}
	})
	err := sameGenesis(a, c)
	if err == nil || !strings.Contains(err.Error(), "app_state.houses") {
		t.Errorf("a pre-enacted upgrade in one of the two must be named: %v", err)
	}
	d := genesisWith(t, func(d map[string]any) { d["initial_height"] = "100" })
	if err := sameGenesis(a, d); err == nil || !strings.Contains(err.Error(), "initial_height") {
		t.Errorf("a top-level difference must be named: %v", err)
	}
	if err := sameGenesis([]byte(`x`), a); err == nil {
		t.Error("garbage was accepted")
	}
}
