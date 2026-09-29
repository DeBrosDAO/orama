package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	gogoproto "github.com/cosmos/gogoproto/proto"
)

func TestHasInclusionCommit(t *testing.T) {
	commit := []byte(inclusionCommitMagic + "\x0a\x00")
	require.True(t, hasInclusionCommit([][]byte{commit, []byte("tx")}))
	require.False(t, hasInclusionCommit(nil), "an empty block has no injected commit")
	require.False(t, hasInclusionCommit([][]byte{[]byte("tx"), commit}), "the commit must be first")
	require.False(t, hasInclusionCommit([][]byte{[]byte("ORAMA-INCLUSION-EXTENDED-COMMIT-V2:")}))
}

func TestAdvanced(t *testing.T) {
	require.True(t, advanced(10, 13, 3))
	require.False(t, advanced(10, 12, 3))
	require.False(t, advanced(10, 10, 3), "a stalled chain")
	require.False(t, advanced(13, 10, 3), "a height that went backwards")
}

func TestInvariantsHold(t *testing.T) {
	ok, broken := invariantsHold(map[string]bool{"a": true, "b": true})
	require.True(t, ok)
	require.Empty(t, broken)

	ok, broken = invariantsHold(map[string]bool{"z": false, "a": false, "m": true})
	require.False(t, ok)
	require.Equal(t, []string{"a", "z"}, broken)

	ok, _ = invariantsHold(nil)
	require.False(t, ok, "a module with no boolean check proves nothing")
	ok, _ = invariantsHold(map[string]bool{})
	require.False(t, ok)
}

// encodeInvariants builds the wire form of a module's Invariants response from field values, using
// the same descriptors the chain serves.
func encodeInvariants(t *testing.T, module string, values map[string]bool) []byte {
	t.Helper()
	svc, err := gogoproto.HybridResolver.FindDescriptorByName(protoreflect.FullName("orama." + module + ".v1.Query"))
	require.NoError(t, err)
	md := svc.(protoreflect.ServiceDescriptor).Methods().ByName("Invariants")
	msg := dynamicpb.NewMessage(md.Output())
	for name, v := range values {
		f := md.Output().Fields().ByName(protoreflect.Name(name))
		require.NotNil(t, f, name)
		msg.Set(f, protoreflect.ValueOfBool(v))
	}
	raw, err := proto.Marshal(msg)
	require.NoError(t, err)
	return raw
}

func TestInvariantBools_decodesEveryModulesResponse(t *testing.T) {
	for _, m := range invariantModules {
		got, err := invariantBools(m, nil)
		require.NoError(t, err, m)
		require.NotEmpty(t, got, "%s has at least one boolean check", m)
		for name, v := range got {
			require.False(t, v, "%s.%s: an empty response is all false", m, name)
		}
	}
}

func TestInvariantBools_readsTrueAndFalseFields(t *testing.T) {
	raw := encodeInvariants(t, "nodes", map[string]bool{"balance_matches": true, "active_roles_bonded": true, "capacity_backed": false})
	got, err := invariantBools("nodes", raw)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"balance_matches": true, "active_roles_bonded": true, "capacity_backed": false}, got)
	ok, broken := invariantsHold(got)
	require.False(t, ok)
	require.Equal(t, []string{"capacity_backed"}, broken)
}

func TestInvariantBools_refusesAnUnknownModuleAndGarbage(t *testing.T) {
	_, err := invariantBools("nosuchmodule", nil)
	require.Error(t, err)
	_, err = invariantBools("nodes", []byte{0xff, 0xff, 0xff})
	require.Error(t, err)
}
