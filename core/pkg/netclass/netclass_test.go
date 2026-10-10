package netclass

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestIsProduction_classifiesByMarker(t *testing.T) {
	cases := map[string]bool{
		"orama-stagenet-6": false, "orama-devnet-1": false, "orama-localnet-3": false,
		"orama-1": true, "orama-mainnet": true, "stagenet": true, "orama-stagenet": true, "": true,
	}
	for id, want := range cases {
		if got := IsProduction(id); got != want {
			t.Errorf("IsProduction(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestCheckCommittee_productionFloor(t *testing.T) {
	if err := CheckCommittee("orama-1", ProductionMinCommittee-1); err == nil {
		t.Fatal("a production chain id with 29 validators was accepted")
	} else if !strings.Contains(err.Error(), "-stagenet-") || !strings.Contains(err.Error(), "30") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
	if err := CheckCommittee("orama-1", ProductionMinCommittee); err != nil {
		t.Errorf("a production chain id with 30 validators was refused: %v", err)
	}
}

func TestCheckCommittee_testNetworkAnySize(t *testing.T) {
	for _, n := range []int{1, 5, 29} {
		if err := CheckCommittee("orama-stagenet-1", n); err != nil {
			t.Errorf("a stagenet with %d validators was refused: %v", n, err)
		}
	}
}

func TestCheckCommittee_empty(t *testing.T) {
	if err := CheckCommittee("orama-stagenet-1", 0); err == nil {
		t.Fatal("an empty committee was accepted")
	}
}

// chainFile reads a file of the chain module, which sits beside core/.
func chainFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile("../../../chain/" + rel)
	if err != nil {
		t.Skipf("the chain sources are not reachable from here: %v", err)
	}
	return string(data)
}

func TestNetclass_matchesChain(t *testing.T) {
	list := regexp.MustCompile(`nonProductionChainIDMarkers = \[\]string\{([^}]*)\}`)
	for _, file := range []string{"x/power/keeper/genesis.go", "x/emission/keeper/genesis.go"} {
		m := list.FindStringSubmatch(chainFile(t, file))
		if m == nil {
			t.Fatalf("%s no longer declares nonProductionChainIDMarkers: update pkg/netclass", file)
		}
		var got []string
		for _, q := range strings.Split(m[1], ",") {
			if s, err := strconv.Unquote(strings.TrimSpace(q)); err == nil {
				got = append(got, s)
			}
		}
		if strings.Join(got, " ") != strings.Join(NonProductionMarkers, " ") {
			t.Errorf("%s has markers %v, pkg/netclass has %v", file, got, NonProductionMarkers)
		}
	}
	floor := regexp.MustCompile(`ProductionMinCommitteeSize uint64 = (\d+)`).FindStringSubmatch(chainFile(t, "x/power/types/params.go"))
	if floor == nil || floor[1] != strconv.Itoa(ProductionMinCommittee) {
		t.Errorf("the chain's production committee floor is %v, pkg/netclass has %d", floor, ProductionMinCommittee)
	}
}
