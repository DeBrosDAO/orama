package types_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func TestValidateName_acceptsDNSLabels(t *testing.T) {
	for _, name := range []string{"abc", "my-node", "node01", "a1b", "x-y-z", strings.Repeat("a", types.MaxNameLen), "123", "zeus"} {
		require.NoError(t, types.ValidateName(name), name)
	}
}

func TestValidateName_refusesWhatIsNotAnUnreservedLowercaseLabel(t *testing.T) {
	cases := map[string]string{
		"too short":             "ab",
		"empty":                 "",
		"too long":              strings.Repeat("a", types.MaxNameLen+1),
		"uppercase":             "MyNode",
		"leading hyphen":        "-node",
		"trailing hyphen":       "node-",
		"underscore":            "my_node",
		"dot makes a subdomain": "my.node",
		"space":                 "my node",
		"non ascii":             "nöde",
		"punycode prefix":       "xn--abc",
		"slash":                 "a/b",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, types.ValidateName(input), types.ErrInvalidName)
		})
	}
}

func TestValidateName_refusesReservedNames(t *testing.T) {
	reserved := []string{
		"seed", "seed1", "seed2", "seed9", "seed10", "www", "api", "ns1", "ns9", "ns12", "admin", "gateway",
		"explorer", "status", "releases", "mail",
	}
	for _, name := range reserved {
		require.True(t, types.IsReservedName(name), name)
		require.ErrorContains(t, types.ValidateName(name), "reserved", name)
	}
}

func TestIsReservedName_onlyTheNumberedFamiliesMatchByPattern(t *testing.T) {
	for _, name := range []string{"seedling", "seeds", "nsx", "nss", "mynode", "seed-1", "1seed"} {
		require.False(t, types.IsReservedName(name), name)
	}
}

func TestReservedNames_everyEntryIsAValidLabelSoTheListCannotShadowItself(t *testing.T) {
	for name := range types.ReservedNames {
		require.Equal(t, strings.ToLower(name), name)
		require.NotEmpty(t, name)
	}
}
