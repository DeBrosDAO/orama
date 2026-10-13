package display

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

// A node's update finding is in the table (column and a sentence with the policy's decision and
// why) and in the JSON (state, policy, reason): `orama status` is where an operator learns a newer
// release exists.
func TestStatus_showsAnAvailableUpdateWithWhatThePolicyWillDo(t *testing.T) {
	snap := contractSnapshot()
	snap.Nodes[0].Report.Update = &updatenotice.Notice{State: updatenotice.StateAvailable, Mode: "notify", Channel: "stable",
		Current: "0.3.0", Candidate: "0.3.1", Reason: "newer release 0.3.1 (notify)"}
	snap.Nodes[1].Report.Update = &updatenotice.Notice{State: updatenotice.StateRefused, Mode: "auto", Channel: "stable",
		Current: "0.3.0", Candidate: "0.3.1", Reason: "this machine is a validator, upgrade it by hand"}

	var table bytes.Buffer
	if err := StatusTable(snap, nil, &table); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"UPDATE", "0.3.1 (notify)", "refused 0.3.1", "\nUpdates\n",
		"release 0.3.1 is available on the stable channel (running 0.3.0); the policy (notify) will notify: newer release 0.3.1 (notify)",
		"release 0.3.1 was refused on the stable channel (running 0.3.0): this machine is a validator, upgrade it by hand"} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("status table misses %q:\n%s", want, table.String())
		}
	}

	var buf bytes.Buffer
	if err := StatusJSON(snap, nil, &buf); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Nodes []struct {
			Update *struct {
				State     string `json:"state"`
				Policy    string `json:"policy"`
				Mode      string `json:"mode"`
				Channel   string `json:"channel"`
				Current   string `json:"current"`
				Candidate string `json:"candidate"`
				Reason    string `json:"reason"`
			} `json:"update"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if u := doc.Nodes[0].Update; u == nil || u.State != "available" || u.Policy != "notify" || u.Candidate != "0.3.1" ||
		u.Current != "0.3.0" || u.Channel != "stable" || u.Mode != "notify" || u.Reason == "" {
		t.Errorf("node 0 update = %+v", u)
	}
	if u := doc.Nodes[1].Update; u == nil || u.State != "refused" || u.Policy != "refuse" || !strings.Contains(u.Reason, "by hand") {
		t.Errorf("node 1 update = %+v", u)
	}
	if doc.Nodes[2].Update != nil {
		t.Errorf("a node with nothing to report has an update: %+v", doc.Nodes[2].Update)
	}
	if strings.Contains(buf.String(), `"update": null`) || strings.Contains(buf.String(), `"update":null`) {
		t.Error("an absent update is written as null")
	}
}

func TestStatus_noUpdateAndNoPlanPrintNoSections(t *testing.T) {
	var table bytes.Buffer
	if err := StatusTable(withChains(contractSnapshot()), nil, &table); err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"Updates", "Scheduled chain upgrade"} {
		if strings.Contains(table.String(), unwanted) {
			t.Errorf("status table has %q with nothing to show:\n%s", unwanted, table.String())
		}
	}
}

// A chain upgrade the chain has scheduled shows with its height, the blocks and time left and
// whether the node holds the binary for it; an unstaged binary is the thing to act on.
func TestStatus_showsAPendingChainUpgradeAndWhetherItIsStaged(t *testing.T) {
	snap := withChains(contractSnapshot())
	snap.Nodes[0].Report.Chain.AvgBlockTimeSec = 6
	snap.Nodes[0].Report.Chain.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 700, Staged: true}
	snap.Nodes[1].Report.Chain.AvgBlockTimeSec = 6
	snap.Nodes[1].Report.Chain.Upgrade = &report.ChainUpgrade{Name: "v0-4-0", Height: 700}
	snap.Nodes[2].Report.Chain.UpgradeError = "HTTP 500"

	var table bytes.Buffer
	if err := StatusTable(snap, nil, &table); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\nScheduled chain upgrade\n",
		"v0-4-0 at height 700 (600 blocks left, about 1h00m); binary staged on this node",
		"v0-4-0 at height 700 (600 blocks left, about 1h00m); binary NOT staged on this node",
		"the scheduled upgrade could not be read: HTTP 500"} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("status table misses %q:\n%s", want, table.String())
		}
	}

	var buf bytes.Buffer
	if err := StatusJSON(snap, nil, &buf); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Healthy bool `json:"healthy"`
		Nodes   []struct {
			Chain struct {
				Upgrade *struct {
					Name            string `json:"name"`
					Height          int64  `json:"height"`
					BlocksRemaining int64  `json:"blocks_remaining"`
					ETASeconds      *int64 `json:"eta_seconds"`
					Staged          bool   `json:"staged"`
				} `json:"upgrade"`
				UpgradeError string `json:"upgrade_error"`
			} `json:"chain"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	staged, unstaged := doc.Nodes[0].Chain.Upgrade, doc.Nodes[1].Chain.Upgrade
	if staged == nil || staged.Name != "v0-4-0" || staged.Height != 700 || staged.BlocksRemaining != 600 ||
		staged.ETASeconds == nil || *staged.ETASeconds != 3600 || !staged.Staged {
		t.Errorf("staged node: %+v", staged)
	}
	if unstaged == nil || unstaged.Staged {
		t.Errorf("unstaged node: %+v", unstaged)
	}
	if doc.Nodes[2].Chain.Upgrade != nil || doc.Nodes[2].Chain.UpgradeError != "HTTP 500" {
		t.Errorf("node whose plan could not be read: %+v", doc.Nodes[2].Chain)
	}
	if !doc.Healthy {
		t.Error("a pending upgrade made the cluster unhealthy; it is a warning alert, not a health state")
	}
}
