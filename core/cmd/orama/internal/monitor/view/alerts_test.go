package view

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

var mixedAlerts = []cluster.Alert{
	{Severity: cluster.AlertInfo, Subsystem: "system", Node: "1.1.1.1", Message: "zombies"},
	{Severity: cluster.AlertWarning, Subsystem: "dns", Node: "2.2.2.2", Message: "cert expires"},
	{Severity: cluster.AlertCritical, Subsystem: "rqlite", Node: "", Message: "no leader"},
	{Severity: cluster.AlertWarning, Subsystem: "dns", Node: "2.2.2.2", Message: "cert expires"},
	{Severity: cluster.AlertCritical, Subsystem: "collection", Node: "3.3.3.3", Message: "SSH failed"},
}

func TestPrepareAlerts_dedupesAndSortsBySeverity(t *testing.T) {
	rows := PrepareAlerts(mixedAlerts, FilterAll)
	var got []string
	for _, r := range rows {
		got = append(got, SeverityTag(r.Severity)+" "+r.Subsystem)
	}
	want := "CRIT collection,CRIT rqlite,WARN dns,INFO system"
	if strings.Join(got, ",") != want {
		t.Fatalf("order = %v, want %s", got, want)
	}
	if rows[2].Count != 2 {
		t.Fatalf("the duplicate warning was not counted: %+v", rows[2])
	}
}

func TestPrepareAlerts_filters(t *testing.T) {
	cases := map[SeverityFilter]int{FilterAll: 4, FilterCritical: 2, FilterWarning: 1, FilterInfo: 1}
	for f, want := range cases {
		rows := PrepareAlerts(mixedAlerts, f)
		if len(rows) != want {
			t.Errorf("filter %s: %d rows, want %d", f.Label(), len(rows), want)
		}
		for _, r := range rows {
			if f != FilterAll && string(r.Severity) != string(f) {
				t.Errorf("filter %s let through %s", f.Label(), r.Severity)
			}
		}
	}
}

func TestPrepareAlerts_empty(t *testing.T) {
	if rows := PrepareAlerts(nil, FilterCritical); len(rows) != 0 {
		t.Fatalf("got %+v", rows)
	}
}

func TestNodeLabel_clusterWide(t *testing.T) {
	if NodeLabel(cluster.Alert{}) != "cluster" || NodeLabel(cluster.Alert{Node: "1.1.1.1"}) != "1.1.1.1" {
		t.Fatal("wrong labels")
	}
}

func TestHint_pointsAtRealCommands(t *testing.T) {
	cases := []struct {
		alert cluster.Alert
		want  []string
	}{
		{cluster.Alert{Subsystem: "rqlite"}, []string{"orama inspect --env devnet --subsystem rqlite", "docs/COMMON_PROBLEMS.md §6"}},
		{cluster.Alert{Subsystem: "wireguard", Node: "1.1.1.1"}, []string{"--subsystem wg", "§1"}},
		{cluster.Alert{Subsystem: cluster.SubsystemCollection, Node: "3.3.3.3"}, []string{"orama monitor node --env devnet --node 3.3.3.3 --ssh"}},
		{cluster.Alert{Subsystem: "service", Node: "2.2.2.2"}, []string{"orama ssh 2.2.2.2 --env devnet 'sudo orama node status'"}},
		{cluster.Alert{Subsystem: "vault", Node: "2.2.2.2"}, []string{"orama monitor node --env devnet --node 2.2.2.2"}},
		{cluster.Alert{Subsystem: "namespace"}, []string{"docs/COMMON_PROBLEMS.md §1–§4"}},
	}
	for _, tc := range cases {
		got := Hint(tc.alert, "devnet")
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("Hint(%s) = %q, missing %q", tc.alert.Subsystem, got, want)
			}
		}
	}
}

func TestHint_nothingToSay(t *testing.T) {
	if got := Hint(cluster.Alert{Subsystem: "gateway"}, "devnet"); got != "" {
		t.Errorf("a cluster-wide gateway alert got a host command: %q", got)
	}
	if got := Hint(cluster.Alert{Subsystem: "something-new"}, "devnet"); got != "" {
		t.Errorf("an unknown subsystem got a hint: %q", got)
	}
}

// A hint is pasted into a shell, so only an IP address is put into one.
func TestHint_nonAddressHostIsLeftOut(t *testing.T) {
	got := Hint(cluster.Alert{Subsystem: cluster.SubsystemCollection, Node: "x; rm -rf ~"}, "devnet")
	if strings.Contains(got, "rm -rf") || got != "orama monitor --env devnet --ssh" {
		t.Fatalf("got %q", got)
	}
	if got := Hint(cluster.Alert{Subsystem: "service", Node: "evil`id`"}, "devnet"); got != "" {
		t.Fatalf("got %q", got)
	}
}
