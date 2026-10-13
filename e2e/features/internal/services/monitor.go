//go:build e2e_fleet

package services

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// rawReport is the part of `orama status report --json` these packages read
// that harness/monitor does not type: each node's subsystem reports
// (core/pkg/telemetry/report/types.go), kept raw.
type rawReport struct {
	Alerts []struct {
		Severity  string `json:"severity"`
		Subsystem string `json:"subsystem"`
		Node      string `json:"node"`
		Message   string `json:"message"`
	} `json:"alerts"`
	Nodes []struct {
		Host   string                     `json:"host"`
		Report map[string]json.RawMessage `json:"report"`
	} `json:"nodes"`
}

// Subsystem runs the operator's monitor report and decodes subsystem key of
// every node that reported into a fresh T, by host.
func Subsystem[T any](t testing.TB, key string) map[string]T {
	t.Helper()
	f := harness.Fleet(t)
	res := harness.CLI(t).MustOK(t, "status", "report", "--env", f.State.Env, "--json")
	var r rawReport
	if err := oramacli.DecodeJSON(res, &r); err != nil {
		t.Fatalf("failed to decode the monitor report: %v", err)
	}
	out := map[string]T{}
	for _, n := range r.Nodes {
		raw, ok := n.Report[key]
		if !ok || n.Report == nil {
			continue
		}
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %s report: %v", n.Host, key, err)
		}
		out[n.Host] = v
	}
	if len(out) == 0 {
		t.Fatalf("no node reported a %q subsystem", key)
	}
	return out
}

// Alerts returns the monitor report's alerts for subsystem, as text.
func Alerts(t testing.TB, subsystem string) []string {
	t.Helper()
	f := harness.Fleet(t)
	res := harness.CLI(t).MustOK(t, "status", "report", "--env", f.State.Env, "--json")
	var r rawReport
	if err := oramacli.DecodeJSON(res, &r); err != nil {
		t.Fatalf("failed to decode the monitor report: %v", err)
	}
	var out []string
	for _, a := range r.Alerts {
		if a.Subsystem == subsystem {
			out = append(out, fmt.Sprintf("%s %s: %s", a.Severity, a.Node, a.Message))
		}
	}
	return out
}
