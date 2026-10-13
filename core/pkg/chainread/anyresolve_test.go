package chainread

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	baseAccountURL   = "/cosmos.auth.v1beta1.BaseAccount"
	moduleAccountURL = "/cosmos.auth.v1beta1.ModuleAccount"
	secp256k1URL     = "/cosmos.crypto.secp256k1.PubKey"
	// bigAccountNumber is above int64: the chain assigns account numbers across the whole uint64
	// range, and protojson answers a uint64 as a string.
	bigAccountNumber = 16083108751219634649
)

func appendBytes(b []byte, num protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func appendString(b []byte, num protowire.Number, v string) []byte {
	return appendBytes(b, num, []byte(v))
}

func appendVarint(b []byte, num protowire.Number, v uint64) []byte {
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

// anyOf encodes a google.protobuf.Any{type_url, value}.
func anyOf(url string, value []byte) []byte {
	return appendBytes(appendString(nil, 1, url), 2, value)
}

// baseAccount encodes cosmos.auth.v1beta1.BaseAccount; a nil pubKey leaves pub_key unset.
func baseAccount(address string, pubKey []byte, number, sequence uint64) []byte {
	b := appendString(nil, 1, address)
	if pubKey != nil {
		b = appendBytes(b, 2, anyOf(secp256k1URL, appendBytes(nil, 1, pubKey)))
	}
	b = appendVarint(b, 3, number)
	return appendVarint(b, 4, sequence)
}

// accountResponse encodes a QueryAccountResponse{account: Any{url, value}}.
func accountResponse(url string, value []byte) []byte { return appendBytes(nil, 1, anyOf(url, value)) }

func decodeAccount(t *testing.T, method string, response []byte) map[string]any {
	t.Helper()
	m, err := Lookup(method)
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.DecodeResponse(response)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("answer %s is not JSON: %v", out, err)
	}
	return got
}

// Bug #744: the Account answer packs a BaseAccount in an Any, and protojson answered an error for
// the type URL because it looked the type up in the global registry, where core links none.
func TestDecodeResponse_accountIsABaseAccount(t *testing.T) {
	const address = "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"
	got := decodeAccount(t, "cosmos.auth.v1beta1.Query/Account", accountResponse(baseAccountURL, baseAccount(address, nil, bigAccountNumber, 7)))
	acct, _ := got["account"].(map[string]any)
	if acct["@type"] != baseAccountURL || acct["address"] != address {
		t.Fatalf("account = %v", acct)
	}
	if acct["account_number"] != "16083108751219634649" || acct["sequence"] != "7" {
		t.Fatalf("account_number %v sequence %v, want uint64 strings", acct["account_number"], acct["sequence"])
	}
}

func TestDecodeResponse_accountWithASecp256k1PubKey(t *testing.T) {
	key := []byte{0x02, 0xaa, 0xbb}
	got := decodeAccount(t, "cosmos.auth.v1beta1.Query/Account", accountResponse(baseAccountURL, baseAccount("orama1x", key, 5, 1)))
	pub, _ := got["account"].(map[string]any)["pub_key"].(map[string]any)
	if pub["@type"] != secp256k1URL || pub["key"] != "Aqq7" {
		t.Fatalf("pub_key = %v, want the secp256k1 key in base64", pub)
	}
}

func TestDecodeResponse_accountIsAModuleAccount(t *testing.T) {
	module := appendString(appendBytes(nil, 1, baseAccount("orama1fee", nil, 3, 0)), 2, "fee_collector")
	module = appendString(module, 3, "burner")
	got := decodeAccount(t, "cosmos.auth.v1beta1.Query/Account", accountResponse(moduleAccountURL, module))
	acct, _ := got["account"].(map[string]any)
	if acct["@type"] != moduleAccountURL || acct["name"] != "fee_collector" {
		t.Fatalf("account = %v", acct)
	}
	base, _ := acct["base_account"].(map[string]any)
	if base["address"] != "orama1fee" {
		t.Fatalf("base_account = %v", base)
	}
}

// Every Any a public wallet query can return must resolve, not only the BaseAccount: the
// Accounts list adds the vesting accounts, a multisig key and the other key types.
func TestDecodeResponse_everyAnyAWalletQueryReturnsResolves(t *testing.T) {
	for _, url := range []string{
		"/cosmos.auth.v1beta1.BaseAccount", "/cosmos.auth.v1beta1.ModuleAccount",
		"/cosmos.vesting.v1beta1.BaseVestingAccount", "/cosmos.vesting.v1beta1.ContinuousVestingAccount",
		"/cosmos.vesting.v1beta1.DelayedVestingAccount", "/cosmos.vesting.v1beta1.PeriodicVestingAccount",
		"/cosmos.vesting.v1beta1.PermanentLockedAccount",
		"/cosmos.crypto.secp256k1.PubKey", "/cosmos.crypto.ed25519.PubKey", "/cosmos.crypto.secp256r1.PubKey",
		"/cosmos.crypto.multisig.LegacyAminoPubKey",
	} {
		resolved, err := anyResolver()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resolved.FindMessageByURL(url); err != nil {
			t.Errorf("%s does not resolve: %v (run gen.sh)", url, err)
		}
	}
}

func TestDecodeResponse_anUnknownAnyTypeIsAnErrorThatNamesIt(t *testing.T) {
	m, _ := Lookup("cosmos.auth.v1beta1.Query/Account")
	_, err := m.DecodeResponse(accountResponse("/cosmos.nope.v1.Account", []byte{0x0a, 0x01, 'x'}))
	if err == nil || !strings.Contains(err.Error(), "/cosmos.nope.v1.Account") {
		t.Fatalf("err = %v, want one naming the type URL", err)
	}
}
