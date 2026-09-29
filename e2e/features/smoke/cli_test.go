//go:build e2e_fleet

package smoke

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// TestVersion_cliMatchesFleet checks that the CLI under test and the gateway
// the fleet runs are the same release: the run installed the archive built
// from the same commit as the CLI.
func TestVersion_cliMatchesFleet(t *testing.T) {
	t.Parallel()
	out := harness.CLI(t).MustOK(t, "version").Stdout
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "orama" {
		t.Fatalf("orama version printed %q, want \"orama <version> ...\"", out)
	}
	var v struct {
		Version string `json:"version"`
	}
	if err := harness.GW(t).MustSend(t, gw.Req{Path: "/v1/version"}).Expect(t, http.StatusOK).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Version != fields[1] {
		t.Fatalf("CLI is %s but the gateway runs %s", fields[1], v.Version)
	}
}

func TestVersion_unknownFlagFails(t *testing.T) {
	t.Parallel()
	res, err := harness.CLI(t).Run(t.Context(), "version", "--no-such-flag")
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit == 0 {
		t.Fatalf("orama version accepted an unknown flag: %s", res.Stdout)
	}
}
