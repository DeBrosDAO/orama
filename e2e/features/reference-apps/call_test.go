//go:build e2e_fleet

package referenceapps

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

const (
	callPeers      = 3
	callPublishers = 2
	// placeBudget bounds the SFU starting on every node after enable
	// (docs/WEBRTC.md "Architecture").
	placeBudget = 5 * time.Minute
	mediaBudget = 2 * time.Minute
	webrtcOff   = 5 * time.Minute
)

// TestReferenceCall_groupCallThroughEveryNode: the call app's front-end is
// served by name and three users in one room, each signalling through a
// different node's gateway, talk relay-only through the namespace's TURN and
// SFU: both publishers are heard by the listener and by each other
// (docs/WEBRTC.md; the headless peer speaks the page's protocol).
func TestReferenceCall_groupCallThroughEveryNode(t *testing.T) {
	t.Parallel()
	tn := realistic.NewTenant(t)
	if len(tn.F.State.Nodes) < callPeers {
		harness.SkipNotApplicable(t, "a WebRTC namespace needs three members; the run has fewer than three nodes")
	}
	tn.N.CLI.MustOK(t, "namespace", "enable", "webrtc", "--namespace", tn.N.Name)
	t.Cleanup(func() { disableWebRTC(t, tn) })
	web := tn.Deploy(t, "static", realistic.CopyApp(t, realistic.AppCallWeb, nil, nil), "call")
	tn.EveryNodeServes(t, web, "/", "reference-call")
	waitSFU(t, tn)
	room := "standup-" + randomTopic(t)[6:14]
	var peers []*services.RTCPeer
	for i, u := range realistic.NewUsers(t, tn, roleRuntime, callPeers) {
		node := tn.F.State.Nodes[i]
		p, err := services.JoinRoom(t.Context(), tn.C.PinTo(node.PublicIP), u.Token(), room, fmt.Sprintf("user-%d", i))
		if err != nil {
			t.Fatalf("user %d joining through %s: %v", i, node.Name, err)
		}
		t.Cleanup(p.Close)
		if err := p.Start(i < callPublishers); err != nil {
			t.Fatalf("user %d: %v", i, err)
		}
		peers = append(peers, p)
	}
	eventually.Require(t, time.Second, mediaBudget, "everyone hears someone", func() (bool, error) {
		for _, p := range peers[:callPublishers] {
			if err := p.Publish(); err != nil {
				return false, err
			}
		}
		for i, p := range peers {
			if p.Received() == 0 {
				return false, fmt.Errorf("peer %d has received nothing (ICE connected=%v)", i, p.Connected())
			}
		}
		return true, nil
	})
}

// waitSFU waits until the namespace's SFU is active on every node.
func waitSFU(t *testing.T, tn *realistic.Tenant) {
	t.Helper()
	unit := "orama-namespace-sfu@" + tn.N.Name + ".service"
	eventually.Require(t, pollEvery, placeBudget, "the SFU on every node", func() (bool, error) {
		for _, n := range tn.F.State.Nodes {
			if s := tn.F.Unit(t, n, unit); s != "active" {
				return false, fmt.Errorf("%s: %s is %s", n.Name, unit, s)
			}
		}
		return true, nil
	})
}

func disableWebRTC(t *testing.T, tn *realistic.Tenant) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), webrtcOff)
	defer cancel()
	if res, err := tn.N.CLI.Run(ctx, "namespace", "disable", "webrtc", "--namespace", tn.N.Name); err != nil || res.Exit != 0 {
		t.Errorf("cleanup: disabling WebRTC on %s: %v %s", tn.N.Name, err, res.Stderr)
	}
}
