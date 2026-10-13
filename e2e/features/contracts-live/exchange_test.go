//go:build e2e_fleet

package contractslive

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// liveCases are the fixtures a test below sends to the fleet. A fixture
// added under contracts/ without a live case fails
// TestContracts_everyFixtureHasALiveCase.
var liveCases = []string{
	"auth/challenge", "auth/verify", "auth/refresh", "auth/logout",
	"cache/put", "cache/get", "cache/scan", "cache/delete",
	"db/create-table", "db/exec", "db/query", "db/find", "db/find-one", "db/transaction", "db/drop-table",
	"pubsub/publish", "storage/pin", "network/proxy-anon",
}

var jsonHeader = http.Header{"Content-Type": {"application/json"}}

// send posts the fixture's request with live values and returns the answer.
func send(t testing.TB, c *gw.Client, bearer string, f fixture, live map[string]any) *gw.Response {
	t.Helper()
	return c.MustSend(t, gw.Req{Method: f.Method, Path: f.Route, Bearer: bearer, Header: jsonHeader, Body: f.body(t, live)})
}

// exchange sends the fixture's request and fails unless the live answer is
// a 2xx whose shape matches the fixture's response. It returns the answer.
func exchange(t testing.TB, c *gw.Client, bearer string, f fixture, live map[string]any) map[string]any {
	t.Helper()
	resp := send(t, c, bearer, f, live)
	if resp.Status/100 != 2 {
		t.Fatalf("%s (%s %s): the live gateway answered %d to the fixture's request: %.300s",
			f.Name, f.Method, f.Route, resp.Status, resp.Body)
	}
	return checkShape(t, f, resp.Body)
}

// checkShape compares a live body with the fixture's response.
func checkShape(t testing.TB, f fixture, body []byte) map[string]any {
	t.Helper()
	var want, got any
	if err := json.Unmarshal(f.Response, &want); err != nil {
		t.Fatalf("%s: fixture response is not JSON: %v", f.Name, err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%s: live answer is not JSON: %v: %.300s", f.Name, err, body)
	}
	problems, extra := shapeDiff("response", want, got)
	for _, p := range problems {
		t.Errorf("%s (sdk %s) drifted from contracts/%s.json: %s", f.Route, f.SDK, f.Name, p)
	}
	if len(extra) > 0 {
		t.Logf("%s: live answer carries members the fixture does not: %s", f.Name, strings.Join(extra, ", "))
	}
	obj, _ := got.(map[string]any)
	return obj
}

// TestContracts_everyFixtureHasALiveCase: every route fixture is exercised
// against the fleet by this package, so a new contract cannot skip the live
// check (contracts/README.md "Adding one").
func TestContracts_everyFixtureHasALiveCase(t *testing.T) {
	t.Parallel()
	fx := loadFixtures(t)
	var missing, stale []string
	have := map[string]bool{}
	for _, name := range liveCases {
		have[name] = true
		if _, ok := fx[name]; !ok {
			stale = append(stale, name)
		}
	}
	for name := range fx {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("fixtures with no live case: %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("live cases whose fixture is gone: %v", stale)
	}
}
