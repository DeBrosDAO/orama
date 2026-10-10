package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// genesisFacts are the fields of a genesis that decide who validates, what exists
// at block 1 and what a test network may do. They are read by name from the JSON
// oramad writes (protobuf JSON: 64-bit integers are strings).
type genesisFacts struct {
	ChainID  string `json:"chain_id"`
	AppState struct {
		Power struct {
			Exported      bool   `json:"exported"`
			GateSatisfied bool   `json:"gate_satisfied"`
			Lambda        string `json:"lambda"`
			Params        struct {
				MinCommitteeSize string `json:"min_committee_size"`
			} `json:"params"`
			BootstrapCommittee []struct {
				OperatorAddress string `json:"operator_address"`
				Moniker         string `json:"moniker"`
				ConsensusPubkey string `json:"consensus_pubkey"`
			} `json:"bootstrap_committee"`
		} `json:"power"`
		Emission struct {
			Params struct {
				AllowBootstrapStake bool `json:"allow_bootstrap_stake"`
				FaucetEnabled       bool `json:"faucet_enabled"`
			} `json:"params"`
		} `json:"emission"`
		Bank struct {
			Balances []json.RawMessage `json:"balances"`
			Supply   []json.RawMessage `json:"supply"`
		} `json:"bank"`
		Genutil struct {
			GenTxs []json.RawMessage `json:"gen_txs"`
		} `json:"genutil"`
	} `json:"app_state"`
	Consensus struct {
		Params struct {
			Block struct {
				MaxGas string `json:"max_gas"`
			} `json:"block"`
			Abci struct {
				VoteExtensionsEnableHeight string `json:"vote_extensions_enable_height"`
			} `json:"abci"`
		} `json:"params"`
	} `json:"consensus"`
}

// VerifyGenesis checks a genesis against the committee it is meant to start, before
// it is published or handed to a machine. The machine that built it is one of the
// seats, and the others take its word: a genesis that adds a validator, a balance
// or a gentx, names a seat twice, or turns on what the chain's class does not allow
// would start a network nobody asked for. The committee must be exactly the seats,
// at zero supply, with the faucet and the bootstrap-stake switch as the class says.
func VerifyGenesis(genesis []byte, spec GenesisSpec) error {
	if err := rejectAmbiguousKeys(genesis); err != nil {
		return fmt.Errorf("the genesis is not the JSON oramad writes: %w", err)
	}
	var g genesisFacts
	if err := json.Unmarshal(genesis, &g); err != nil {
		return fmt.Errorf("the genesis is not the JSON oramad writes: %w", err)
	}
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if g.ChainID != spec.ChainID {
		add("it is for chain %q, not %q", g.ChainID, spec.ChainID)
	}
	for _, p := range committeeProblems(g, spec.Seats) {
		add("%s", p)
	}
	if e := g.AppState.Emission.Params; e.AllowBootstrapStake != spec.TestNetwork || e.FaucetEnabled != spec.Faucet {
		add("emission allow_bootstrap_stake=%t faucet_enabled=%t, want %t and %t for this chain id", e.AllowBootstrapStake, e.FaucetEnabled, spec.TestNetwork, spec.Faucet)
	}
	if n := len(g.AppState.Bank.Balances) + len(g.AppState.Bank.Supply) + len(g.AppState.Genutil.GenTxs); n != 0 {
		add("it has balances, a supply or gentxs: a bootstrap-committee genesis starts at exactly zero supply")
	}
	if p := g.AppState.Power; p.Exported || p.GateSatisfied || !isZero(p.Lambda) {
		add("x/power starts from an exported state (exported=%t gate_satisfied=%t lambda=%q): a new chain starts at lambda 0 from its committee", p.Exported, p.GateSatisfied, p.Lambda)
	}
	problems = append(problems, filledModuleLists(genesis)...)
	if c := g.Consensus.Params; c.Block.MaxGas != consensusMaxGas || c.Abci.VoteExtensionsEnableHeight != voteExtensionsEnableHeight {
		add("consensus max_gas=%q vote_extensions_enable_height=%q, want %q and %q", c.Block.MaxGas, c.Abci.VoteExtensionsEnableHeight, consensusMaxGas, voteExtensionsEnableHeight)
	}
	if len(problems) > 0 {
		return fmt.Errorf("the genesis is not the one this committee should start:\n  - %s\n"+
			"  if it was built for another committee and no chain has run, build a new one with --force-new-genesis; "+
			"to add machines to a network that runs, join it with `orama setup --network <name>`", strings.Join(problems, "\n  - "))
	}
	return nil
}

// committeeProblems compares the genesis's bootstrap committee with the seats: the
// same accounts with the same keys and monikers, none twice, none extra.
func committeeProblems(g genesisFacts, seats []Seat) []string {
	var problems []string
	members := g.AppState.Power.BootstrapCommittee
	if size, err := strconv.Atoi(g.AppState.Power.Params.MinCommitteeSize); err != nil || size != len(seats) {
		problems = append(problems, fmt.Sprintf("min_committee_size is %q, want %d", g.AppState.Power.Params.MinCommitteeSize, len(seats)))
	}
	if len(members) != len(seats) {
		problems = append(problems, fmt.Sprintf("it names %d bootstrap validators, want the %d seats", len(members), len(seats)))
	}
	byAddress := map[string]int{}
	for _, m := range members {
		byAddress[m.OperatorAddress]++
	}
	for _, s := range seats {
		found := false
		for _, m := range members {
			if m.OperatorAddress == s.Address {
				found = true
				if m.ConsensusPubkey != s.ConsensusPubKey || m.Moniker != s.Moniker {
					problems = append(problems, fmt.Sprintf("seat %s (account %s) has consensus key %q and moniker %q, want %q and %q",
						s.Moniker, s.Address, m.ConsensusPubkey, m.Moniker, s.ConsensusPubKey, s.Moniker))
				}
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("seat %s (account %s, consensus key %s) is not in it", s.Moniker, s.Address, s.ConsensusPubKey))
		}
	}
	for address, n := range byAddress {
		if n > 1 {
			problems = append(problems, fmt.Sprintf("account %q is a bootstrap validator %d times", address, n))
		}
	}
	return problems
}

// checkSeatsDistinct refuses a committee in which two machines report the same
// node id, consensus key or account: they would be one validator, or a node
// pretending to be two.
func checkSeatsDistinct(seats []Seat) error {
	seen := map[string]string{}
	for _, s := range seats {
		for kind, value := range map[string]string{"node id": s.NodeID, "consensus key": s.ConsensusPubKey, "seat account": s.Address} {
			key := kind + " " + value
			if other, dup := seen[key]; dup {
				return fmt.Errorf("machines %s and %s report the same %s (%s): they are one validator, not two", other, s.Moniker, kind, value)
			}
			seen[key] = s.Moniker
		}
	}
	return nil
}

// isZero reports whether a decimal string is zero.
func isZero(decimal string) bool {
	f, err := strconv.ParseFloat(decimal, 64)
	return err == nil && f == 0
}

// listsAllowed are the lists a new network's genesis fills, by module: the
// committee, the bank's denomination metadata, and the standard contracts
// (add-standard-contracts stores their codes and the wasm sequences, and lists
// their ids in wasmpolicy). Every other list of every module starts empty: a
// validator, an account, an operator, a node, a token, a pool, an enacted decision
// or a contract instance in a genesis was put there by whoever wrote it.
var listsAllowed = map[string][]string{
	"bank":       {"denom_metadata"},
	"power":      {"bootstrap_committee"},
	"wasm":       {"codes", "sequences"},
	"wasmpolicy": {"genesis_code_ids"},
}

// filledModuleLists names the lists of app_state that are not empty and are not
// allowed to be.
func filledModuleLists(genesis []byte) []string {
	var doc struct {
		AppState map[string]json.RawMessage `json:"app_state"`
	}
	if err := json.Unmarshal(genesis, &doc); err != nil {
		return nil
	}
	var problems []string
	for module, raw := range doc.AppState {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		for field, value := range fields {
			var list []json.RawMessage
			if json.Unmarshal(value, &list) != nil || len(list) == 0 || slices.Contains(listsAllowed[module], field) {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s.%s has %d entries: a new network starts with only its committee, the denomination metadata and the standard contracts", module, field, len(list)))
		}
	}
	slices.Sort(problems)
	return problems
}

// maxKeyCheckDepth is how deep objects are checked for keys that differ only in
// case or underscores: the document, app_state, a module, and a module's own
// messages are where a field name decides what the chain reads. The same key twice
// in one object is refused at any depth.
const maxKeyCheckDepth = 5

// rejectAmbiguousKeys refuses a document that gives one field two names. The CLI
// reads the genesis with Go's encoding/json and the chain with protobuf JSON, which
// also accepts a field's lowerCamelCase name; a genesis with both "bootstrap_committee"
// and "bootstrapCommittee" (or a key twice) would show the CLI one committee and start
// the chain with another.
func rejectAmbiguousKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := walkKeys(dec, 1); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("there is more after the document")
	}
	return nil
}

func walkKeys(dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	seen, exact := map[string]string{}, map[string]bool{}
	for dec.More() {
		if delim == '{' {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := keyTok.(string)
			if exact[key] {
				return fmt.Errorf("the key %q is there twice", key)
			}
			exact[key] = true
			norm := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if prev, dup := seen[norm]; dup && depth <= maxKeyCheckDepth {
				return fmt.Errorf("the keys %q and %q name the same field", prev, key)
			}
			seen[norm] = key
		}
		if err := walkKeys(dec, depth+1); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}
