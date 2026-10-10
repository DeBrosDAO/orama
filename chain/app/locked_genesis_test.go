package app_test

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/DeBrosOfficial/network/chain/app"
	shieldedpool "github.com/DeBrosOfficial/network/chain/x/shielded/pool"
)

// lockedRow is one locked genesis parameter: its plan value and where the plan
// states it. Rows tagged "code default" have no number in the plan: the plan
// names the parameter and G1's model has not signed a different value, so the
// value in code at this commit is the launch value.
type lockedRow struct {
	module, key, want, cite string
}

const (
	citeD14   = "plans/open-network.md D14, track-c C2"
	citeD16   = "plans/open-network.md D16, track-c C4"
	citeG1    = "track-g G1: the plan gives no number, so the code default is the launch value until the G1 model signs one"
	citeC7    = "track-c C7 (structure only); " + citeG1
	citeC8    = "track-c C8 (structure only); " + citeG1
	citeC5G4  = "track-c C5, track-g G4"
	citeSec   = "chain security review (code default)"
	citeStake = "plans/open-network.md D16, track-c C4 (staking unbonding, slashing)"
)

var lockedRows = []lockedRow{
	// x/emission: D12, D13, C3.
	{"emission", "epoch_duration_seconds", "86400", "track-c C3: an epoch is 24 hours of BFT time"},
	{"emission", "min_blocks_per_epoch", "14400", "track-c C3: minimum blocks per epoch (code default, production floor)"},
	{"emission", "allow_bootstrap_stake", "false", "plans/open-network.md D12: zero premine"},
	{"emission", "faucet_enabled", "false", "test-network faucet: off on every production chain (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md \"Test-network faucet\")"},
	{"emission", "faucet_max_drip", "1000000000000", "test-network faucet, 1,000 ORAMA (code default)"},
	{"emission", "faucet_epoch_cap", "100000000000000", "test-network faucet, 100 max drips per epoch (code default)"},
	{"emission", "faucet_recipient_cooldown_seconds", "86400", "test-network faucet, 24 hours (code default)"},

	// x/fees: D14, C2.
	{"fees", "target_block_gas_fraction", "0.5", "track-c C2: base fee targets 50% fullness"},
	{"fees", "max_base_fee_change_fraction", "0.125", "track-c C2: at most 12.5% per block"},
	{"fees", "min_base_fee", "1", "P2 fee floor: 1 norama/gas placeholder; the dollar peg needs a testnet price (G1)"},
	{"fees", "initial_base_fee", "1", "P2 fee floor (equal to min_base_fee at genesis)"},
	{"fees", "deposit_refund_fraction", "0.99", "plans/open-network.md D14: 99% of a state deposit is refunded"},
	{"fees", "deposit_burn_fraction", "0.01", "plans/open-network.md D14: 1% of a state deposit is burned"},

	// x/power: D15, D16, C4, P1, P7.
	{"power", "min_committee_size", "30", "plans/open-network.md D16: at least 30 bootstrap validators"},
	{"power", "bootstrap_exit_stake", "271000000000000", "P1: 5% of year-1 emission, 271,000 ORAMA"},
	{"power", "bootstrap_deadline_epochs", "365", "P1, D16: about 12 months of daily epochs"},
	{"power", "cap_fraction_normal", "0.05", "plans/open-network.md D15: 5% cap"},
	{"power", "cap_fraction_reduced", "0.03", "plans/open-network.md D15: 3% cap above 60 validators"},
	{"power", "cap_step_down_validator_count", "60", "plans/open-network.md D15: step down past 60 validators"},
	{"power", "cap_step_up_validator_count", "50", "track-c C4: hysteresis returns to 5% below 50"},
	{"power", "cap_hysteresis_epochs", "30", "track-c C4: for 30 days"},
	{"power", "ramp_epochs", "30", "plans/open-network.md D15: 30-day ramp"},
	{"power", "force_bond_fraction", "0.5", "plans/open-network.md D16: 50% of committee rewards force-bonded"},
	{"power", "self_bond_cap_multiplier", "2", "track-c C4: self-bond target 2x the minimum"},
	{"power", "min_self_bond", "1000000000000", "1,000 ORAMA; " + citeG1},
	{"power", "useful_work_multiplier", "1", "plans/open-network.md D15: M coded at 1.0"},
	{"power", "useful_work_multiplier_activated", "false", "plans/open-network.md D15: M off until a two-house vote"},
	{"power", "comet_power_scale", "1000000000", citeSec},
	{"power", "max_redistribution_multiplier", "2", citeSec + " H3(b)"},
	{"power", "pre_gate_lambda_cap", "0.95", citeSec + " H3(a)"},
	{"power", "min_delegation_for_rewards", "1000000000", citeSec + " M2: 1 ORAMA"},

	// x/nodes: C6, P3.
	{"nodes", "min_bond", `[{"role":"ROLE_VALIDATOR","amount":"1000000000"},{"role":"ROLE_STORAGE","amount":"1000000000"},{"role":"ROLE_RELAY","amount":"1000000000"},{"role":"ROLE_EXIT","amount":"1000000000"},{"role":"ROLE_DIRAUTH","amount":"1000000000"},{"role":"ROLE_ARCHIVER","amount":"1000000000"}]`, "track-c C6, track-g G1 min_bond[role]: 1 ORAMA per role; " + citeG1},
	{"nodes", "bond_per_gib", "1000000000", "track-g G1 bond_per_gib: 1 ORAMA; " + citeG1},
	{"nodes", "unbonding_seconds", "1814400", "plans/open-network.md D16: 21 days"},
	{"nodes", "deposit_per_byte", "68359", "P3: about 0.07 ORAMA/KiB"},
	{"nodes", "probation_capacity_bytes", "1073741824", "track-c C2 probation capacity (1 GiB); " + citeG1},
	{"nodes", "min_service_volume_bytes", "1", "track-c C5 proven service volume (1 byte); " + citeG1},
	{"nodes", "max_endpoints", "8", "track-c C6 record bound (code default)"},
	{"nodes", "max_bindings", "8", "track-c C6 record bound (code default)"},
	{"nodes", "network_identity_lock_seconds", "1209600", "14 days, the D17 parameter timelock; track-c C5/C7 give no number for how long a declared /16 and ASN must stand (launch default), " + citeG1},

	{"nodes", "name_deposit", "1000000000", "epic 3306 D1: 1 ORAMA locked per identification name, returned on release or retire; " + citeG1},

	// x/storage: C7, G1.
	{"storage", "min_deal_bytes", "1024", citeC7},
	{"storage", "deal_fee", "1000", citeC7},
	{"storage", "max_deals_per_block", "100", citeC7},
	{"storage", "k_c", "8", citeC7},
	{"storage", "miss_threshold", "4", citeC7},
	{"storage", "max_settlements_per_block", "100", citeC7},
	{"storage", "s_min_providers", "8", citeC7},
	{"storage", "s_full_providers", "32", citeC7},
	{"storage", "accept_window_blocks", "50", citeC7},
	{"storage", "probation_slots", "1", citeC7},
	{"storage", "probation_operator_cap", "2", citeC7},
	{"storage", "probation_network16_cap", "2", citeC7},
	{"storage", "probation_asn_cap", "2", citeC7},
	{"storage", "probation_expiry_epochs", "4", citeC7},
	{"storage", "probation_deposit", "1000", citeC7},
	{"storage", "max_releases_per_epoch", "2", citeC7},
	{"storage", "protocol_every_epochs", "0", citeC7},
	{"storage", "slash_fraction", "0.1", citeC7},
	{"storage", "protocol_piece_bytes", "1024", citeC7},
	{"storage", "protocol_price_per_epoch", "1000", citeC7},
	{"storage", "protocol_duration_epochs", "1", citeC7},

	// x/relay: C8, G1.
	{"relay", "min_reporters_quorum", "3", "owner decision on track-c C8: all 3 initial dirauths, so one lying reporter cannot move a relay's pay"},
	{"relay", "min_uptime_fraction", "0.9", citeC8},
	{"relay", "exit_multiplier", "2", citeC8},
	{"relay", "per_relay_cap", "100000000000", citeC8},
	{"relay", "per_operator_cap", "200000000000", citeC8},
	{"relay", "per_prefix16_cap", "200000000000", citeC8},

	// x/token: C10, P3, P4.
	{"token", "creation_fee", "10000000000", "P4: about $10-25 in ORAMA, burned; 10 ORAMA at genesis (no price peg before a testnet price exists)"},
	{"token", "deposit_per_byte", "68359", "P3: about 0.07 ORAMA/KiB"},

	// x/archive: C14, D22.
	{"archive", "retention_window_blocks", "201600", "plans/open-network.md D22: validators keep 14 days of blocks (6-second blocks)"},
	{"archive", "max_piece_bytes", "4294967296", "track-c C14 (structure only): 4 GiB caps the bundle file an attestation commits to; " + citeG1},
	{"archive", "max_candidates_per_range", "4", "track-c C14 (structure only): bounds the conflicting tuples one undecided range keeps, an honest one, wrong ones and a fork; " + citeG1},
	{"archive", "range_blocks", "1000", "track-c C14 (structure only): every archived range is exactly this many blocks and starts at a multiple of it plus 1, so ranges never overlap; " + citeG1},

	// x/shielded: C12, D19, D20, P5. The plan fixes the 2% cap (P5, a constant in x/shielded/pool) and
	// names the other parameters without numbers; C12a's spec text does not exist yet, so each is a
	// launch default until the G1 model and the C12a spec sign one.
	{"shielded", "anchor_window_blocks", "14400", "track-c C12a names the anchor window without a number; 24 hours of 6-second blocks (launch default)"},
	{"shielded", "nullifier_fee", "1000000", "track-c C12: a burned per-nullifier fee with no number; 0.001 ORAMA; " + citeG1},
	{"shielded", "action_gas", "250000", "track-c C12: the per-action verify gas is unmeasured (C0-4); " + citeG1},
	{"shielded", "max_actions_per_bundle", "16", "track-c C12 (structure only): bounds proof work per bundle (launch default)"},
	{"shielded", "unshield_floor", "1000000000", "P5: the cap is max(2% of the pool, a floor); 1 ORAMA; " + citeG1},
	{"shielded", "max_fee_topup", "10000000", "track-c C12: max_fee_topup per unshield to the fee balance, no number; 0.01 ORAMA; " + citeG1},
	{"shielded", "queue_per_address_cap", "100000000000", "track-c C12: the per-address limit of the unshield queue, no number; 100 ORAMA; " + citeG1},
	{"shielded", "max_signerless_per_block", "64", "track-c C12 (structure only): bounds signer-less proof work per block (launch default)"},

	// x/houses: D17, C5, G4.
	{"houses", "bootstrap_exit_stake", "271000000000000", "P1: 271,000 ORAMA"},
	{"houses", "token_quorum", "0.4", citeC5G4},
	{"houses", "token_pass_threshold", "0.5", citeC5G4},
	{"houses", "voting_period_seconds", "604800", citeC5G4 + ": 7 days"},
	{"houses", "house_bond", "1000000000000", citeC5G4 + ": 1,000 ORAMA; " + citeG1},
	{"houses", "max_eligible_per_prefix16", "3", citeC5G4},
	{"houses", "max_eligible_per_asn", "5", citeC5G4},
	{"houses", "min_house_size", "21", "plans/open-network.md D17, track-g G1: min_house_size 21"},
	{"houses", "veto_window_seconds", "604800", "plans/open-network.md D17: the operator house vetoes within 7 days"},
	{"houses", "parameter_timelock_seconds", "1209600", "plans/open-network.md D17: 14 days for parameters"},
	{"houses", "upgrade_timelock_seconds", "5184000", "plans/open-network.md D17: 60 days for upgrades"},
	{"houses", "spend_timelock_seconds", "604800", "plans/open-network.md D17: 7 days for fund spending"},

	// x/staking: D16, C4, P7.
	{"staking", "bond_denom", `"norama"`, "plans/open-network.md D12: norama is the bond denom"},
	{"staking", "unbonding_time", `"1814400s"`, citeStake},
	{"staking", "max_validators", "100", "P7 is not chosen (track-c C0-2 pending); the stock value 100 is the top of the plan's 60-100 range"},
	{"staking", "max_entries", "7", "stock x/staking default"},
	{"staking", "historical_entries", "10000", "stock x/staking default"},
	{"staking", "min_commission_rate", "0", "stock x/staking default"},

	// x/slashing: C4.
	{"slashing", "slash_fraction_double_sign", "0.05", "plans/open-network.md D16: double-sign costs 5%"},
	{"slashing", "slash_fraction_downtime", "0.0001", "plans/open-network.md D16: downtime costs 0.01%"},
	{"slashing", "signed_blocks_window", "10000", "chain security review B3 (code default)"},
	{"slashing", "min_signed_per_window", "0.5", "stock x/slashing default"},
	{"slashing", "downtime_jail_duration", `"600s"`, "stock x/slashing default"},

	// x/distribution: D13, C2.
	{"distribution", "community_tax", "0", "track-c C2: community_tax = 0"},
	{"distribution", "base_proposer_reward", "0", "stock x/distribution (deprecated field)"},
	{"distribution", "bonus_proposer_reward", "0", "stock x/distribution (deprecated field)"},
	{"distribution", "withdraw_addr_enabled", "true", "stock x/distribution default"},

	// x/wasmpolicy: D11, P6.
	{"wasmpolicy", "upload_sunset_height", "3162240", "P6: 183 days of 5-second blocks; D11"},
	{"wasmpolicy", "deposit_per_byte", "68359", "P3: about 0.07 ORAMA/KiB; C9 state deposits use the same price as x/token and x/nodes"},
	{"wasmpolicy", "max_deposit_per_tx", "10000000000", "G1 launch default (C9 security review): 10 ORAMA of contract state deposit per transaction"},
	{"wasmpolicy", "max_deposit_chunks", "32", "G1 launch default (C9 security review): one deposit row per payer, at most 32 per contract, bounds a shrink"},
	{"wasmpolicy", "chunk_overhead_bytes", "512", "G1 launch default (C9 security review): prices the ledger rows a new payer adds to a contract"},
}

// planParameters maps P1-P10 to where each one is locked.
var planParameters = []struct{ id, locked string }{
	{"P1", "power.bootstrap_exit_stake, power.bootstrap_deadline_epochs, houses.bootstrap_exit_stake"},
	{"P2", "fees.min_base_fee, fees.initial_base_fee"},
	{"P3", "nodes.deposit_per_byte, token.deposit_per_byte, wasmpolicy.deposit_per_byte"},
	{"P4", "token.creation_fee"},
	{"P5", "x/shielded/pool UnshieldNumerator/UnshieldDenominator (ossified constants, not a genesis field)"},
	{"P6", "wasmpolicy.upload_sunset_height"},
	{"P7", "staking.max_validators (stock 100; P7 not chosen)"},
	{"P8", "off-chain: no genesis field (network and app names)"},
	{"P9", "off-chain: no genesis field (public IPFS DHT policy)"},
	{"P10", "off-chain: no genesis field (VPN pricing)"},
}

func lockedRowKey(r lockedRow) string { return r.module + "." + r.key }

func relaxedOnTestnets() map[string]bool {
	return map[string]bool{
		"emission.epoch_duration_seconds":            true,
		"emission.min_blocks_per_epoch":              true,
		"emission.allow_bootstrap_stake":             true,
		"emission.faucet_enabled":                    true,
		"emission.faucet_max_drip":                   true,
		"emission.faucet_epoch_cap":                  true,
		"emission.faucet_recipient_cooldown_seconds": true,
		"power.min_committee_size":                   true,
	}
}

// moduleObject returns the JSON object G1 locks for a row's module: the
// module's params, or its whole genesis for wasmpolicy.
func moduleObject(t *testing.T, gs app.GenesisState, module string) map[string]json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(gs[module], &obj))
	if module == "wasmpolicy" {
		return obj
	}
	var params map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(obj["params"], &params))
	return params
}

func sameValue(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	g := strings.Trim(string(got), `"`)
	w := strings.Trim(want, `"`)
	gd, gerr := math.LegacyNewDecFromStr(g)
	wd, werr := math.LegacyNewDecFromStr(w)
	if gerr == nil && werr == nil {
		return gd.Equal(wd)
	}
	return g == w
}

func cloneGenesis(t *testing.T, gs app.GenesisState) app.GenesisState {
	t.Helper()
	bz, err := json.Marshal(gs)
	require.NoError(t, err)
	var out app.GenesisState
	require.NoError(t, json.Unmarshal(bz, &out))
	return out
}

// mutateParam returns gs with row's parameter changed to a different value.
func mutateParam(t *testing.T, gs app.GenesisState, row lockedRow) app.GenesisState {
	t.Helper()
	out := cloneGenesis(t, gs)
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out[row.module], &obj))
	target := obj
	var params map[string]json.RawMessage
	if row.module != "wasmpolicy" {
		require.NoError(t, json.Unmarshal(obj["params"], &params))
		target = params
	}
	target[row.key] = differentValue(t, target[row.key])
	if row.module != "wasmpolicy" {
		pbz, err := json.Marshal(params)
		require.NoError(t, err)
		obj["params"] = pbz
	}
	bz, err := json.Marshal(obj)
	require.NoError(t, err)
	out[row.module] = bz
	return out
}

func differentValue(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	s := string(raw)
	switch {
	case s == "true":
		return json.RawMessage("false")
	case s == "false":
		return json.RawMessage("true")
	case strings.HasPrefix(s, "["):
		return json.RawMessage("[]")
	}
	trimmed := strings.Trim(s, `"`)
	if d, err := math.LegacyNewDecFromStr(trimmed); err == nil {
		return json.RawMessage(`"` + d.Add(math.LegacyOneDec()).String() + `"`)
	}
	return json.RawMessage(`"` + trimmed + `-changed"`)
}

func TestLockedGenesis_defaultsEqualPlanValues(t *testing.T) {
	defaults := buildTestApp(t).DefaultGenesis()
	for _, row := range lockedRows {
		t.Run(lockedRowKey(row), func(t *testing.T) {
			require.NotEmpty(t, row.cite, "every locked parameter needs its plan citation")
			got, ok := moduleObject(t, defaults, row.module)[row.key]
			require.True(t, ok, "%s is not in the default genesis", lockedRowKey(row))
			require.Truef(t, sameValue(t, got, row.want), "%s default is %s, plan value %s (%s)", lockedRowKey(row), got, row.want, row.cite)
		})
	}
}

func TestLockedGenesis_everyGenesisParameterHasARow(t *testing.T) {
	defaults := buildTestApp(t).DefaultGenesis()
	covered := map[string]bool{}
	for _, row := range lockedRows {
		covered[lockedRowKey(row)] = true
	}
	modules := []string{"emission", "fees", "power", "nodes", "storage", "relay", "token", "archive", "houses", "staking", "slashing", "distribution"}
	var missing []string
	for _, module := range modules {
		for key := range moduleObject(t, defaults, module) {
			if !covered[module+"."+key] {
				missing = append(missing, module+"."+key)
			}
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "new genesis parameters must be added to lockedRows with a plan citation")
	for key := range moduleObject(t, defaults, "wasmpolicy") {
		if key == "genesis_code_ids" || key == "deposit_chunks" {
			continue // contents of the genesis, not parameters
		}
		require.True(t, covered["wasmpolicy."+key], "wasmpolicy.%s needs a lockedRows entry", key)
	}
}

func TestLockedGenesis_planParametersP1ToP10(t *testing.T) {
	require.Len(t, planParameters, 10)
	for i, p := range planParameters {
		require.Equal(t, "P"+strconv.Itoa(i+1), p.id)
		require.NotEmpty(t, p.locked)
	}
	require.Equal(t, int64(2), shieldedpool.UnshieldNumerator)
	require.Equal(t, int64(100), shieldedpool.UnshieldDenominator)
}

func TestLockedGenesis_productionChainRejectsEveryChangedParameter(t *testing.T) {
	defaults := buildTestApp(t).DefaultGenesis()
	require.NoError(t, app.ValidateLockedGenesis("orama-1", defaults, cloneGenesis(t, defaults)))
	for _, row := range lockedRows {
		t.Run(lockedRowKey(row), func(t *testing.T) {
			err := app.ValidateLockedGenesis("orama-1", defaults, mutateParam(t, defaults, row))
			require.Error(t, err)
			require.Contains(t, err.Error(), lockedRowKey(row))
		})
	}
}

func TestLockedGenesis_testnetRelaxesOnlyTheClockAndCommittee(t *testing.T) {
	defaults := buildTestApp(t).DefaultGenesis()
	relaxed := relaxedOnTestnets()
	for _, chainID := range []string{"orama-stagenet-1", "orama-devnet-1"} {
		for _, row := range lockedRows {
			err := app.ValidateLockedGenesis(chainID, defaults, mutateParam(t, defaults, row))
			if relaxed[lockedRowKey(row)] {
				require.NoErrorf(t, err, "%s on %s", lockedRowKey(row), chainID)
			} else {
				require.Errorf(t, err, "%s on %s", lockedRowKey(row), chainID)
			}
		}
	}
}

func TestLockedGenesis_localnetLocksNothing(t *testing.T) {
	defaults := buildTestApp(t).DefaultGenesis()
	for _, row := range lockedRows {
		require.NoErrorf(t, app.ValidateLockedGenesis("orama-localnet-1", defaults, mutateParam(t, defaults, row)), lockedRowKey(row))
	}
}

func TestLockedGenesis_numericFormsCompareEqual(t *testing.T) {
	defaults := buildTestApp(t).DefaultGenesis()
	gs := cloneGenesis(t, defaults)
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(gs["power"], &obj))
	var params map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(obj["params"], &params))
	params["cap_fraction_normal"] = json.RawMessage(`"0.05"`)
	params["ramp_epochs"] = json.RawMessage(`30`)
	pbz, err := json.Marshal(params)
	require.NoError(t, err)
	obj["params"] = pbz
	gs["power"], err = json.Marshal(obj)
	require.NoError(t, err)
	require.NoError(t, app.ValidateLockedGenesis("orama-1", defaults, gs))
}

func TestLockedGenesis_missingModuleAndMissingParams(t *testing.T) {
	defaults := buildTestApp(t).DefaultGenesis()

	gs := cloneGenesis(t, defaults)
	delete(gs, "houses")
	require.ErrorContains(t, app.ValidateLockedGenesis("orama-1", defaults, gs), `module "houses" is missing`)

	gs = cloneGenesis(t, defaults)
	gs["fees"] = json.RawMessage(`{}`)
	require.ErrorContains(t, app.ValidateLockedGenesis("orama-1", defaults, gs), `module "fees" has no params`)
}

func TestLockedGenesis_initChainRefusesChangedParameterOnProductionChainID(t *testing.T) {
	app.SetAddressPrefixes()
	const prodChainID = "orama-1"
	oramaApp := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(prodChainID))
	gs := mutateParam(t, oramaApp.DefaultGenesis(), lockedRow{module: "fees", key: "min_base_fee"})
	bz, err := json.Marshal(gs)
	require.NoError(t, err)

	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId:       prodChainID,
		InitialHeight: 1,
		Time:          time.Unix(1_700_000_000, 0),
		AppStateBytes: bz,
	})
	require.ErrorContains(t, err, "fees.min_base_fee")
}
