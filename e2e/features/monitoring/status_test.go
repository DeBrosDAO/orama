//go:build e2e_fleet

package monitoring

import (
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// healthy is `orama status`'s status of a serving node
// (core/pkg/telemetry/cluster/snapshot.go HealthHealthy).
const healthy = "healthy"

// TestStatus_everyNodeHealthyAsTableAndJSON: `orama status` lists every core
// node healthy — as a table, and with --json as {host, role, status}
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama status").
func TestStatus_everyNodeHealthyAsTableAndJSON(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	table := cli.MustOK(t, "status", "--env", f.State.Env).Stdout
	var rows []struct{ Host, Role, Status, Error string }
	monitorJSONArgs(t, &rows, "status", "--env", f.State.Env, "--json")
	for _, n := range f.State.Nodes {
		found := false
		for _, r := range rows {
			found = found || (r.Host == n.PublicIP && r.Status == healthy && r.Error == "" && r.Role != "")
		}
		if !found {
			t.Errorf("status does not list %s (%s) healthy: %+v", n.Name, n.PublicIP, rows)
		}
		if !regexp.MustCompile(regexp.QuoteMeta(n.PublicIP)).MatchString(table) {
			t.Errorf("the status table does not name %s", n.PublicIP)
		}
	}
}

// TestNodes_listsTheFleetWithRoles: `orama nodes --json` lists every core
// node of the environment with its role and SSH user (docs/whitepaper/technical-reference/appendices/d-cli-reference.md
// "orama nodes").
func TestNodes_listsTheFleetWithRoles(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var rows []map[string]string
	monitorJSONArgs(t, &rows, "nodes", "--env", f.State.Env, "--json")
	for _, n := range f.State.Nodes {
		i := slices.IndexFunc(rows, func(r map[string]string) bool { return r["ip"] == n.PublicIP })
		if i < 0 {
			t.Errorf("nodes does not list %s (%s): %v", n.Name, n.PublicIP, rows)
			continue
		}
		if r := rows[i]; r["environment"] != f.State.Env || r["role"] == "" || r["user"] == "" {
			t.Errorf("%s listed as %v", n.Name, r)
		}
	}
}

// componentStates are the public component states (website/src/docs/operator/monitoring.mdx
// "Components").
var componentStates = []string{"operational", "degraded", "outage", "unknown"}

var dayFormat = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// minutesPerDay bounds a day's sampled minutes.
const minutesPerDay = 24 * 60

type publicStatus struct {
	Status   string         `json:"status"`
	Server   map[string]any `json:"server"`
	Overall  string         `json:"overall"`
	Headline string         `json:"headline"`
	Nodes    struct {
		Total, Healthy, Unknown int
	} `json:"nodes"`
	Components []struct {
		ID, Name, State, Summary string
		UptimePct                *float64 `json:"uptime_pct"`
		History                  []struct {
			Date      string  `json:"date"`
			UptimePct float64 `json:"uptime_pct"`
			Minutes   int64   `json:"minutes"`
		} `json:"history"`
	} `json:"components"`
}

// TestPublicStatus_uptimeHistoryShape: /v1/status carries the public view:
// the overall state, node counts without names, and each service with a
// state, an uptime share (or none yet) and a history of UTC days, oldest
// first, each with a share and the minutes sampled (website/src/docs/operator/monitoring.mdx
// "Public status page", "Uptime history").
func TestPublicStatus_uptimeHistoryShape(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var s publicStatus
	if err := harness.GW(t).MustSend(t, gw.Req{Path: "/v1/status"}).Expect(t, http.StatusOK).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Status != "ok" || s.Server["started_at"] == nil || !slices.Contains(componentStates, s.Overall) || s.Headline == "" {
		t.Errorf("envelope %+v", s)
	}
	if s.Nodes.Total != len(f.State.Nodes) || s.Nodes.Healthy > s.Nodes.Total {
		t.Errorf("nodes %+v, want total %d", s.Nodes, len(f.State.Nodes))
	}
	ids := []string{}
	for _, c := range s.Components {
		ids = append(ids, c.ID)
		if !slices.Contains(componentStates, c.State) || c.Name == "" || (c.UptimePct != nil && (*c.UptimePct < 0 || *c.UptimePct > 100)) {
			t.Errorf("component %+v", c)
		}
		for i, d := range c.History {
			if !dayFormat.MatchString(d.Date) || d.UptimePct < 0 || d.UptimePct > 100 || d.Minutes < 1 || d.Minutes > minutesPerDay ||
				(i > 0 && d.Date <= c.History[i-1].Date) {
				t.Errorf("%s history day %d: %+v (want YYYY-MM-DD ascending, 0-100%%, 1-%d minutes)", c.ID, i, d, minutesPerDay)
			}
		}
	}
	for _, want := range []string{"gateway", "database", "cache", "storage", "dns", "mesh"} {
		if !slices.Contains(ids, want) {
			t.Errorf("no %s component in %v", want, ids)
		}
	}
}
