package types_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// allowedMessageFields is the entire governance surface. A field that is not
// on this list cannot be added to a message, which is how an ossified rule
// (emission schedule and tail, burn rule, privacy-by-default, freeze,
// blacklist, halt, and the absence of an authority, pause, circuit breaker
// or expedited path) stays unreachable.
var allowedMessageFields = map[string]bool{
	"proposer":                  true,
	"content":                   true,
	"parameter_change":          true,
	"software_upgrade":          true,
	"emission_split":            true,
	"development_spend":         true,
	"power_bounds":              true,
	"relay_reporters":           true,
	"allow_list":                true,
	"proposal_id":               true,
	"voter":                     true,
	"option":                    true,
	"signer":                    true,
	"amount":                    true,
	"token_quorum":              true,
	"token_pass_threshold":      true,
	"voting_period_seconds":     true,
	"house_bond":                true,
	"max_eligible_per_prefix16": true,
	"max_eligible_per_asn":      true,
	"name":                      true,
	"height":                    true,
	"validator_percent":         true,
	"storage_percent":           true,
	"relay_percent":             true,
	"development_percent":       true,
	"recipient":                 true,
	"epoch":                     true,
	"m_max":                     true,
	"activate_m":                true,
	"add":                       true,
	"remove":                    true,
	"code_upload_add":           true,
	"code_upload_remove":        true,
	"adapter_add":               true,
	"adapter_remove":            true,
}

// forbiddenFragments are the ossified concepts. None may appear in a message
// field name even if someone also extends the allow-list without reading it.
var forbiddenFragments = []string{
	"expedit",
	"blacklist",
	"freeze",
	"privacy",
	"halt",
	"pause",
	"circuit",
	"authority",
	"multisig",
	"admin",
	"tail",
	"schedule",
	"burn",
	"kill",
	"bootstrap_exit",
}

func TestNoMessageReachesAnOssifiedField(t *testing.T) {
	reg := codectypes.NewInterfaceRegistry()
	require.NotPanics(t, func() { types.RegisterInterfaces(reg) })

	seen := map[reflect.Type]bool{}
	var fields []string
	for _, msg := range types.GovernanceMsgs() {
		walkProtoFields(reflect.TypeOf(msg), seen, &fields)
	}
	require.NotEmpty(t, fields)
	for _, name := range fields {
		require.Truef(t, allowedMessageFields[name], "message field %q is not on the governance allow-list", name)
		lower := strings.ToLower(name)
		for _, bad := range forbiddenFragments {
			require.NotContainsf(t, lower, bad, "message field %q reaches an ossified concept", name)
		}
	}
	for _, msg := range types.GovernanceMsgs() {
		name := reflect.TypeOf(msg).Elem().Name()
		for _, bad := range []string{"Expedit", "Freeze", "Blacklist", "Pause", "Circuit", "Authority", "Halt", "Tail", "Privacy"} {
			require.NotContains(t, name, bad)
		}
	}
}

func TestMsgServiceHasNoExpeditedOrAuthorityMethod(t *testing.T) {
	for _, m := range types.MsgServiceMethods() {
		lower := strings.ToLower(m)
		require.NotContains(t, lower, "expedit")
		require.NotContains(t, lower, "authority")
		require.NotContains(t, lower, "pause")
		require.NotContains(t, lower, "halt")
	}
}

func walkProtoFields(t reflect.Type, seen map[reflect.Type]bool, out *[]string) {
	if t == nil {
		return
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || strings.HasPrefix(f.Name, "XXX_") {
			continue
		}
		if name, ok := protoName(f); ok {
			*out = append(*out, name)
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		walkProtoFields(ft, seen, out)
	}
}

func protoName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("protobuf")
	if tag == "" {
		return "", false
	}
	for _, part := range strings.Split(tag, ",") {
		if strings.HasPrefix(part, "name=") {
			return strings.TrimPrefix(part, "name="), true
		}
	}
	return "", false
}
