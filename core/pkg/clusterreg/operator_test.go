package clusterreg

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// fields reads every field of a protobuf message into field number -> values,
// bytes fields as their raw bytes.
func fields(t *testing.T, msg []byte) map[protowire.Number][][]byte {
	t.Helper()
	out := map[protowire.Number][][]byte{}
	for len(msg) > 0 {
		num, typ, n := protowire.ConsumeTag(msg)
		if n < 0 {
			t.Fatalf("bad tag: %v", protowire.ParseError(n))
		}
		msg = msg[n:]
		if typ != protowire.BytesType {
			t.Fatalf("field %d has wire type %d, want bytes", num, typ)
		}
		v, n := protowire.ConsumeBytes(msg)
		if n < 0 {
			t.Fatalf("bad bytes field %d: %v", num, protowire.ParseError(n))
		}
		out[num] = append(out[num], v)
		msg = msg[n:]
	}
	return out
}

func validCreate() ValidatorCreate {
	return ValidatorCreate{
		Operator: vectorOperator, Moniker: "node-a", Website: "https://example.org",
		CommissionRate: "0.10", CommissionMaxRate: "0.20", CommissionMaxStep: "0.01",
		MinSelfDelegation: "1", SelfBond: "1000000000000",
		ConsensusPubKey: bytes.Repeat([]byte{7}, ConsensusPubKeyLen),
	}
}

func TestEncodeRegisterOperator_isTheOperatorAlone(t *testing.T) {
	got, err := EncodeRegisterOperator(vectorOperator)
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, got)
	if len(f) != 1 || string(f[1][0]) != vectorOperator {
		t.Fatalf("fields = %v", f)
	}
}

func TestEncodeRegisterOperator_refusesABadOperator(t *testing.T) {
	for _, op := range []string{"", "orama1xyz", vectorValidator} {
		if _, err := EncodeRegisterOperator(op); err == nil {
			t.Errorf("EncodeRegisterOperator(%q) succeeded", op)
		}
	}
}

func TestEncodeCreateValidator_decodesToTheStakingMessage(t *testing.T) {
	v := validCreate()
	got, err := EncodeCreateValidator(v)
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, got)
	desc := fields(t, f[1][0])
	if string(desc[1][0]) != "node-a" || string(desc[3][0]) != "https://example.org" || len(desc) != 2 {
		t.Errorf("description = %v", desc)
	}
	commission := fields(t, f[2][0])
	for field, want := range map[protowire.Number]string{1: "100000000000000000", 2: "200000000000000000", 3: "10000000000000000"} {
		if string(commission[field][0]) != want {
			t.Errorf("commission field %d = %s, want %s", field, commission[field][0], want)
		}
	}
	if string(f[3][0]) != "1" {
		t.Errorf("min self delegation = %s", f[3][0])
	}
	if _, set := f[4]; set {
		t.Error("the deprecated delegator_address was set")
	}
	if string(f[5][0]) != vectorValidator {
		t.Errorf("validator address = %s, want %s", f[5][0], vectorValidator)
	}
	pubAny := fields(t, f[6][0])
	if string(pubAny[1][0]) != ConsensusPubKeyTypeURL || !bytes.Equal(fields(t, pubAny[2][0])[1][0], v.ConsensusPubKey) {
		t.Errorf("pubkey any = %v", pubAny)
	}
	coin := fields(t, f[7][0])
	if string(coin[1][0]) != "norama" || string(coin[2][0]) != "1000000000000" {
		t.Errorf("value = %v", coin)
	}
}

func TestValidateValidatorCreate_refusals(t *testing.T) {
	for name, edit := range map[string]func(*ValidatorCreate){
		"no moniker":              func(v *ValidatorCreate) { v.Moniker = "  " },
		"moniker too long":        func(v *ValidatorCreate) { v.Moniker = strings.Repeat("a", maxMonikerLen+1) },
		"moniker with escape":     func(v *ValidatorCreate) { v.Moniker = "a\x1b[31m" },
		"short consensus key":     func(v *ValidatorCreate) { v.ConsensusPubKey = v.ConsensusPubKey[:31] },
		"nil consensus key":       func(v *ValidatorCreate) { v.ConsensusPubKey = nil },
		"zero self bond":          func(v *ValidatorCreate) { v.SelfBond = "0" },
		"self bond with decimals": func(v *ValidatorCreate) { v.SelfBond = "1.5" },
		"no min self delegation":  func(v *ValidatorCreate) { v.MinSelfDelegation = "" },
		"rate above 1":            func(v *ValidatorCreate) { v.CommissionRate = "1.5" },
		"rate above its maximum":  func(v *ValidatorCreate) { v.CommissionRate = "0.30" },
		"negative step":           func(v *ValidatorCreate) { v.CommissionMaxStep = "-0.1" },
		"valoper as operator":     func(v *ValidatorCreate) { v.Operator = vectorValidator },
	} {
		t.Run(name, func(t *testing.T) {
			v := validCreate()
			edit(&v)
			if _, err := EncodeCreateValidator(v); err == nil {
				t.Fatal("EncodeCreateValidator accepted it")
			}
		})
	}
}

func TestValidateValidatorCreate_zeroCommissionIsValid(t *testing.T) {
	v := validCreate()
	v.CommissionRate, v.CommissionMaxRate, v.CommissionMaxStep = "0", "0", "0"
	got, err := EncodeCreateValidator(v)
	if err != nil {
		t.Fatalf("a zero commission was refused: %v", err)
	}
	if string(fields(t, fields(t, got)[2][0])[1][0]) != "0" {
		t.Error("a zero rate is not the integer 0")
	}
}

// createValidatorWireHex was produced by cosmos-sdk v0.54.4's
// stakingtypes.MsgCreateValidator.Marshal for validCreate's values (moniker,
// website, the 0.10/0.20/0.01 commission, minimum 1, 1,000 ORAMA, a consensus
// key of seven bytes repeated), the version chain/go.mod pins.
const createValidatorWireHex = "0a1d0a066e6f64652d611a1368747470733a2f2f6578616d706c652e6f7267123b0a1231303030303030303030303030303030303012123230303030303030303030303030303030301a1131303030303030303030303030303030301a01312a336f72616d6176616c6f7065723139726c34636d32686d7238616679346b6c6470787a33666b61346a67757130616c3278756c7332430a1d2f636f736d6f732e63727970746f2e656432353531392e5075624b657912220a2007070707070707070707070707070707070707070707070707070707070707073a170a066e6f72616d61120d31303030303030303030303030"

func TestEncodeCreateValidator_matchesTheSDKWire(t *testing.T) {
	got, err := EncodeCreateValidator(validCreate())
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != createValidatorWireHex {
		t.Fatalf("create validator wire\n got %x\nwant %s", got, createValidatorWireHex)
	}
}
