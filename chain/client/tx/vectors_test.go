package tx_test

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	bankdefs "github.com/cosmos/cosmos-sdk/x/bank/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	oramatx "github.com/DeBrosOfficial/network/chain/client/tx"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// The fixtures are the cross-language contract between this builder and the
// TypeScript one in sdk/src/chain. The TypeScript tests rebuild each case and
// must produce these exact bytes, and must list every message type below.
// Regenerate with: go test ./client/tx -run TestVectors -update-tx-vectors
var updateVectors = flag.Bool("update-tx-vectors", false, "rewrite client/tx/testdata from the current builder")

const (
	vectorsPath  = "testdata/tx_vectors.json"
	msgTypesPath = "testdata/wallet_msgs.json"

	vectorChainID       = "orama-vectors-1"
	vectorAccountNumber = 7
	vectorSequence      = 3
	vectorGas           = 200000
	vectorFee           = "5000"
)

type vectorCase struct {
	Name         string          `json:"name"`
	TypeURL      string          `json:"type_url"`
	Msg          json.RawMessage `json:"msg"`
	Memo         string          `json:"memo,omitempty"`
	BodyBytes    string          `json:"body_bytes_hex"`
	AuthInfoByte string          `json:"auth_info_bytes_hex"`
	SignDoc      string          `json:"sign_doc_hex"`
	Signature    string          `json:"signature_hex"`
	Tx           string          `json:"tx_hex"`
}

type vectorFile struct {
	Comment       string       `json:"comment"`
	ChainID       string       `json:"chain_id"`
	PrivateKey    string       `json:"private_key_hex"`
	Address       string       `json:"address"`
	PubKey        string       `json:"pubkey_hex"`
	AccountNumber uint64       `json:"account_number"`
	Sequence      uint64       `json:"sequence"`
	GasLimit      uint64       `json:"gas_limit"`
	FeeNorama     string       `json:"fee_norama"`
	Cases         []vectorCase `json:"cases"`
}

type msgTypeList struct {
	Comment   string   `json:"comment"`
	OramaMsgs []string `json:"orama_msgs"`
	SDKMsgs   []string `json:"sdk_msgs"`
}

// walletSDKMsgs are the non-Orama messages a wallet signs, and the TypeScript
// registry must decode for an approval screen.
var walletSDKMsgs = []string{
	"/cosmos.bank.v1beta1.MsgSend",
	"/cosmos.staking.v1beta1.MsgCreateValidator",
	"/cosmos.staking.v1beta1.MsgEditValidator",
	"/cosmos.staking.v1beta1.MsgDelegate",
	"/cosmos.staking.v1beta1.MsgUndelegate",
	"/cosmos.staking.v1beta1.MsgBeginRedelegate",
	"/cosmos.slashing.v1beta1.MsgUnjail",
	"/cosmos.distribution.v1beta1.MsgWithdrawDelegatorReward",
	"/cosmwasm.wasm.v1.MsgExecuteContract",
	"/cosmwasm.wasm.v1.MsgInstantiateContract",
}

func norama(n int64) sdk.Coins { return sdk.NewCoins(sdk.NewInt64Coin("norama", n)) }

func addrOf(seed byte, size int) sdk.AccAddress {
	b := make([]byte, size)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return sdk.AccAddress(b)
}

func bytesOf(seed byte, n int) []byte { return []byte(addrOf(seed, n)) }

// vectorMessages are the messages the fixtures cover, all signed by signer.
// They mix every wire shape the builder meets: strings, uint64s, bools, bytes,
// repeated scalars, repeated and nested messages, enums and coin lists.
func vectorMessages(signer string) []struct {
	name string
	msg  sdk.Msg
	memo string
} {
	other := addrOf(0x40, 20).String()
	valoper := sdk.ValAddress(mustAccAddressBytes(signer)).String()
	otherVal := sdk.ValAddress(addrOf(0x50, 20)).String()
	leaf := cnfttypes.Leaf{AssetId: bytesOf(1, 32), Owner: signer, Delegate: other, MetadataCid: "bafybeigdyrzt", CreatorHash: bytesOf(9, 32), Nonce: 4, HashId: 1}
	proof := cnfttypes.MerkleProof{Root: bytesOf(3, 32), Index: 5, Siblings: [][]byte{bytesOf(4, 32), bytesOf(5, 32)}}
	return []struct {
		name string
		msg  sdk.Msg
		memo string
	}{
		{"bank_send", &bankdefs.MsgSend{FromAddress: signer, ToAddress: other, Amount: norama(1000)}, ""},
		{"bank_send_with_memo", &bankdefs.MsgSend{FromAddress: signer, ToAddress: other, Amount: norama(1)}, "invoice 42"},
		{"staking_delegate", &stakingtypes.MsgDelegate{DelegatorAddress: signer, ValidatorAddress: otherVal, Amount: sdk.NewInt64Coin("norama", 5000)}, ""},
		{"staking_begin_redelegate", &stakingtypes.MsgBeginRedelegate{DelegatorAddress: signer, ValidatorSrcAddress: otherVal, ValidatorDstAddress: valoper, Amount: sdk.NewInt64Coin("norama", 77)}, ""},
		{"staking_undelegate", &stakingtypes.MsgUndelegate{DelegatorAddress: signer, ValidatorAddress: otherVal, Amount: sdk.NewInt64Coin("norama", 9)}, ""},
		{"slashing_unjail", &slashingtypes.MsgUnjail{ValidatorAddr: valoper}, ""},
		{"distribution_withdraw_reward", &distrtypes.MsgWithdrawDelegatorReward{DelegatorAddress: signer, ValidatorAddress: otherVal}, ""},
		{"wasm_execute", &wasmtypes.MsgExecuteContract{Sender: signer, Contract: addrOf(0x60, 32).String(), Msg: []byte(`{"ping":{}}`), Funds: norama(12)}, ""},
		{"token_create", &tokentypes.MsgCreateToken{Creator: signer, Subdenom: "gold", Name: "Gold", Symbol: "GLD", Description: "a token", Mint: true, Freeze: true, PermanentDelegate: other, TransferFeeBps: 25, NonTransferable: false, Pause: true, TransferHook: addrOf(0x41, 20).String()}, ""},
		{"token_mint", &tokentypes.MsgMint{Sender: signer, Denom: "factory/" + signer + "/gold", Recipient: other, Amount: math.NewInt(123456789012345)}, ""},
		{"nodes_register_node", &nodestypes.MsgRegisterNode{
			Operator: signer, NodeId: "node-1", Roles: []nodestypes.Role{nodestypes.RoleValidator, nodestypes.RoleStorage},
			HotKey: other,
			Bindings: []nodestypes.Binding{
				{Service: "chain", KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: bytesOf(1, 33), Signature: bytesOf(2, 64)},
				{Service: "tor", KeyType: nodestypes.KeyTypeEd25519, Pubkey: bytesOf(3, 32), Signature: bytesOf(4, 64)},
			},
			Endpoints: []string{"https://a.example", "https://b.example"}, RegionHint: "eu-west", Asn: 64512,
		}, ""},
		{"nodes_fund_hot_key", &nodestypes.MsgFundHotKey{Operator: signer, NodeId: "node-1", Amount: math.NewInt(2500)}, ""},
		{"nodes_register_cluster", &nodestypes.MsgRegisterCluster{Operator: signer, ClusterId: "c1", BaseDomain: "example.org", PublicEndpoints: []string{"https://c1.example.org"}, MetadataUri: "https://example.org/meta.json"}, ""},
		{"storage_create_deal", &storagetypes.MsgCreateDeal{
			Signer: signer, Granter: other, Class: storagetypes.DealClass_DEAL_CLASS_PRIVATE, DealNonce: bytesOf(7, 16),
			RepairDelegate: other, Replicas: 3, PricePerEpoch: math.NewInt(1000), DurationEpochs: 52,
			Pieces: []storagetypes.PieceCommitment{{Root: bytesOf(1, 32), RealLeafCount: 10, PaddedLeafCount: 16, PieceBytes: 1 << 20}, {Root: bytesOf(2, 32), RealLeafCount: 1, PaddedLeafCount: 1, PieceBytes: 4096}},
		}, ""},
		{"storage_grant_deal_authorization", &storagetypes.MsgGrantDealAuthorization{Signer: signer, Grantee: other, SpendLimit: math.NewInt(1_000_000), PeriodEpochs: 4, MaxPieceBytes: 1 << 30, MaxDurationEpochs: 100, Replicas: 3, ExpiryEpoch: 999}, ""},
		{"houses_vote_token", &housetypes.MsgVoteToken{Voter: signer, ProposalId: 12, Option: housetypes.VoteOption_YES}, ""},
		{"market_bid", &markettypes.MsgBid{Bidder: signer, ListingId: 8, Amount: math.NewInt(500)}, ""},
		{"cnft_transfer", &cnfttypes.MsgTransfer{Signer: signer, TreeId: 2, Current: leaf, NewOwner: other, NewDelegate: "", Proof: proof}, ""},
		{"archive_attest", &archivetypes.MsgAttest{Archiver: signer, StartHeight: 100, EndHeight: 200, BundleCid: "bafyarchive", BundleHash: bytesOf(1, 32), MerkleRoot: bytesOf(2, 32), NodeId: "node-1"}, ""},
		{"shielded_shield", &shieldedtypes.MsgShield{Signer: signer, Bundle: bytesOf(1, 300)}, ""},
		{"shielded_shield_earnings", &shieldedtypes.MsgShieldEarnings{Signer: signer, Bundle: bytesOf(2, 300)}, ""},
		{"shielded_unshield_bond", &shieldedtypes.MsgUnshield{Signer: signer, Bundle: bytesOf(3, 300), Target: shieldedtypes.UnshieldTargetBond, Validator: otherVal}, ""},
		{"shielded_unshield_node_bond", &shieldedtypes.MsgUnshield{Signer: signer, Bundle: bytesOf(4, 300), Target: shieldedtypes.UnshieldTargetNodeBond, NodeId: "node-1", Role: nodestypes.RoleStorage}, ""},
		{"relay_register_relay", &relaytypes.MsgRegisterRelay{Operator: signer, NodeId: "node-1", RsaFingerprint: bytesOf(1, 32), Exit: true, Ed25519Signature: bytesOf(2, 64)}, ""},
	}
}

func mustAccAddressBytes(bech32 string) []byte {
	addr, err := sdk.AccAddressFromBech32(bech32)
	if err != nil {
		panic(err)
	}
	return addr
}

// camelKeys rewrites snake_case JSON object keys to lowerCamelCase, the names
// ts-proto's fromJSON reads.
func camelKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[camel(k)] = camelKeys(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = camelKeys(t[i])
		}
	}
	return v
}

func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func buildVectors(t *testing.T) vectorFile {
	t.Helper()
	b := newBuilder(t)
	leaf := mustLeaf(t, abandonMnemonic)
	acc, err := oramatx.DeriveAccount(leaf)
	require.NoError(t, err)
	cdc := codec.NewProtoCodec(oramaInterfaceRegistry(t))

	file := vectorFile{
		Comment:       "Generated by chain/client/tx vectors_test.go. Do not edit; run go test ./client/tx -run TestVectors -update-tx-vectors.",
		ChainID:       vectorChainID,
		PrivateKey:    hex.EncodeToString(leaf),
		Address:       acc.Address,
		PubKey:        vectorPubKey,
		AccountNumber: vectorAccountNumber,
		Sequence:      vectorSequence,
		GasLimit:      vectorGas,
		FeeNorama:     vectorFee,
	}
	for _, vm := range vectorMessages(acc.Address) {
		raw, err := b.Build(acc, oramatx.Unsigned{
			ChainID: vectorChainID, AccountNumber: vectorAccountNumber, Sequence: vectorSequence,
			GasLimit: vectorGas, Fee: norama(5000), Msgs: []sdk.Msg{vm.msg}, Memo: vm.memo,
		})
		require.NoError(t, err, vm.name)

		var txRaw sdktx.TxRaw
		require.NoError(t, txRaw.Unmarshal(raw), vm.name)
		require.Len(t, txRaw.Signatures, 1)
		signDoc, err := (&sdktx.SignDoc{
			BodyBytes: txRaw.BodyBytes, AuthInfoBytes: txRaw.AuthInfoBytes,
			ChainId: vectorChainID, AccountNumber: vectorAccountNumber,
		}).Marshal()
		require.NoError(t, err)

		msgJSON, err := cdc.MarshalJSON(vm.msg)
		require.NoError(t, err, vm.name)
		var generic any
		require.NoError(t, json.Unmarshal(msgJSON, &generic))
		camelJSON, err := json.Marshal(camelKeys(generic))
		require.NoError(t, err)

		file.Cases = append(file.Cases, vectorCase{
			Name: vm.name, TypeURL: sdk.MsgTypeURL(vm.msg), Msg: camelJSON, Memo: vm.memo,
			BodyBytes: hex.EncodeToString(txRaw.BodyBytes), AuthInfoByte: hex.EncodeToString(txRaw.AuthInfoBytes),
			SignDoc: hex.EncodeToString(signDoc), Signature: hex.EncodeToString(txRaw.Signatures[0]), Tx: hex.EncodeToString(raw),
		})
	}
	return file
}

func writeOrCompare(t *testing.T, path string, v any) {
	t.Helper()
	want, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	want = append(want, '\n')
	if *updateVectors {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, want, 0o644))
		return
	}
	got, err := os.ReadFile(path)
	require.NoError(t, err, "missing fixture: run go test ./client/tx -run TestVectors -update-tx-vectors")
	require.Equal(t, string(want), string(got), "%s is stale: the builder's bytes changed. Regenerate with -update-tx-vectors and update sdk/src/chain if the change is intended", path)
}

// TestVectors_fixturesMatchTheBuilder pins the SIGN_MODE_DIRECT bytes of a
// spread of messages. The TypeScript builder is tested against the same file.
func TestVectors_fixturesMatchTheBuilder(t *testing.T) {
	file := buildVectors(t)
	require.GreaterOrEqual(t, len(file.Cases), 15)
	writeOrCompare(t, vectorsPath, file)
}

// TestVectors_signaturesVerify checks each fixture against the chain's own
// verifier, so a fixture can never pin bytes the chain would reject.
func TestVectors_signaturesVerify(t *testing.T) {
	b := newBuilder(t)
	registry := oramaInterfaceRegistry(t)
	registered := map[string]bool{}
	for _, url := range registry.ListImplementations(sdk.MsgInterfaceProtoName) {
		registered[url] = true
	}
	for _, c := range buildVectors(t).Cases {
		// A nowasm build does not register the CosmWasm messages, so the
		// chain would not decode them either; the encoding is still pinned.
		if !registered[c.TypeURL] && strings.HasPrefix(c.TypeURL, "/cosmwasm.") {
			continue
		}
		raw, err := hex.DecodeString(c.Tx)
		require.NoError(t, err)
		decoded, err := b.Decode(raw, vectorChainID, vectorAccountNumber)
		require.NoError(t, err, c.Name)
		require.Equal(t, []string{c.TypeURL}, decoded.MsgTypeURLs, c.Name)
	}
}

// TestVectors_walletMessageList records every Orama message type the chain
// registers and the SDK messages a wallet signs. The TypeScript registry has
// to decode all of them.
func TestVectors_walletMessageList(t *testing.T) {
	registry := oramaInterfaceRegistry(t)
	all := registry.ListImplementations(sdk.MsgInterfaceProtoName)
	registered := map[string]bool{}
	var orama []string
	for _, url := range all {
		registered[url] = true
		if strings.HasPrefix(url, "/orama.") {
			orama = append(orama, url)
		}
	}
	sort.Strings(orama)
	require.NotEmpty(t, orama)
	for _, url := range walletSDKMsgs {
		// The wasm messages are registered only in a build that links CosmWasm.
		if !registered[url] && !strings.HasPrefix(url, "/cosmwasm.") {
			t.Errorf("wallet message %s is not registered by the chain", url)
		}
	}
	writeOrCompare(t, msgTypesPath, msgTypeList{
		Comment:   "Generated by chain/client/tx vectors_test.go. Every orama Msg the chain registers, and the SDK messages a wallet signs.",
		OramaMsgs: orama,
		SDKMsgs:   walletSDKMsgs,
	})
}
