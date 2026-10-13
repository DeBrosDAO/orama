package upgrades

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
)

// examplePlan is what a plan's file exports. It is test-only: no release enacts it.
var examplePlan = Upgrade{
	Name: "example-plan",
	CreateHandler: func(Deps) upgradetypes.UpgradeHandler {
		return func(_ context.Context, _ upgradetypes.Plan, from module.VersionMap) (module.VersionMap, error) {
			return from, nil
		}
	},
	StoreUpgrades: &storetypes.StoreUpgrades{Added: []string{"examplestore"}},
}

func TestRegistry_hasNoDuplicateNames(t *testing.T) {
	require.NoError(t, Validate(Registered()), "the registry the binary ships must be valid")
}

func TestValidate_refusesADuplicateName(t *testing.T) {
	err := Validate([]Upgrade{examplePlan, examplePlan})
	require.ErrorContains(t, err, `"example-plan" is registered twice`)
}

func TestValidate_refusesBadNamesAndMissingHandlers(t *testing.T) {
	for name, u := range map[string]Upgrade{
		"an empty name":     {CreateHandler: examplePlan.CreateHandler},
		"an uppercase name": {Name: "V2", CreateHandler: examplePlan.CreateHandler},
		"a path":            {Name: "../v2", CreateHandler: examplePlan.CreateHandler},
		"no handler":        {Name: "v2"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, Validate([]Upgrade{u}))
		})
	}
	require.NoError(t, Validate(nil), "an empty registry is valid: no plan exists yet")
}

func TestRegistered_isACopy(t *testing.T) {
	got := Registered()
	got = append(got, examplePlan)
	require.Len(t, got, len(registered)+1)
	require.Len(t, Registered(), len(registered), "changing the returned list does not change the registry")
}

func TestLookup_andStoreUpgradesFor(t *testing.T) {
	list := []Upgrade{examplePlan, {Name: "no-stores", CreateHandler: examplePlan.CreateHandler}}
	u, ok := Lookup(list, "example-plan")
	require.True(t, ok)
	require.Equal(t, "example-plan", u.Name)
	_, ok = Lookup(list, "missing")
	require.False(t, ok)

	require.Equal(t, []string{"examplestore"}, StoreUpgradesFor(list, "example-plan").Added)
	require.Nil(t, StoreUpgradesFor(list, "no-stores"))
	require.Nil(t, StoreUpgradesFor(list, "missing"))
}

func TestCheckRelease_aDeclaredPlanNeedsAHandler(t *testing.T) {
	list := []Upgrade{examplePlan}
	require.NoError(t, CheckRelease(list, ""), "a release that enacts no plan needs no handler")
	require.NoError(t, CheckRelease(list, "example-plan"))
	require.ErrorContains(t, CheckRelease(list, "v9"), `no handler is registered for it`)
	require.ErrorContains(t, CheckRelease(list, "V9"), "cosmovisor can stage")
	require.ErrorContains(t, CheckRelease([]Upgrade{examplePlan, examplePlan}, ""), "registered twice")
	require.ErrorContains(t, CheckRelease(nil, "example-plan"), "no handler is registered")
}

func TestParseCurrent(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"empty":                {"", ""},
		"only comments":        {"# nothing\n#\n", ""},
		"a plan":               {"v0.4.0\n", "v0.4.0"},
		"a plan after comment": {"# the plan\n\n  v0.4.0  \n# more\nother\n", "v0.4.0"},
		"no trailing newline":  {"v1", "v1"},
		"crlf":                 {"# c\r\nv1\r\n", "v1"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, parseCurrent(tc.in))
		})
	}
}

// This is the release-time check (make -C chain upgrade-check): the plan the release declares in
// CURRENT must have a registered handler.
func TestRelease_theDeclaredPlanHasARegisteredHandler(t *testing.T) {
	require.NoError(t, CheckRelease(Registered(), Current()))
}

func TestCurrent_isTheEmbeddedFile(t *testing.T) {
	data, err := os.ReadFile("CURRENT")
	require.NoError(t, err)
	require.Equal(t, string(data), currentFile)
	require.False(t, strings.ContainsAny(Current(), " \t"), "a plan name has no whitespace")
}
