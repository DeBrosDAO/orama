//go:build e2e_fleet

package webrtc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// joinPeer joins room through member i's gateway as a fresh runtime member,
// and starts relay-only media.
func joinPeer(t *testing.T, fx *fixture, i int, room string, publish bool) *services.RTCPeer {
	t.Helper()
	c := fx.c.PinTo(fx.members[i%len(fx.members)].PublicIP)
	token := member(t, fx.n, "runtime")
	p, err := services.JoinRoom(t.Context(), c, token, room, "user-"+strconv.Itoa(i))
	if err != nil {
		t.Fatalf("peer %d joining %s: %v", i, room, err)
	}
	t.Cleanup(p.Close)
	if p.Creds.TTL != int(sfuTTL.Seconds()) || !strings.HasSuffix(p.Creds.Username, ":"+fx.n.Name) {
		t.Errorf("SFU-signalled credential %q ttl %d, want :%s with ttl %d", p.Creds.Username, p.Creds.TTL, fx.n.Name, int(sfuTTL.Seconds()))
	}
	if err := p.Start(publish); err != nil {
		t.Fatalf("peer %d: %v", i, err)
	}
	return p
}

// waitMedia publishes from pubs until every sub has received RTP.
func waitMedia(t *testing.T, pubs, subs []*services.RTCPeer) {
	t.Helper()
	eventually.Require(t, time.Second, mediaBudget, "RTP relayed through TURN and the SFU", func() (bool, error) {
		for _, p := range pubs {
			if err := p.Publish(); err != nil {
				return false, err
			}
		}
		for i, s := range subs {
			if s.Received() == 0 {
				return false, fmt.Errorf("subscriber %d has received nothing (ICE connected=%v)", i, s.Connected())
			}
		}
		return true, nil
	})
}

// TestMedia_oneToOneRelayOnly: a publisher on the first member's gateway and a
// subscriber on the second's, both relay-only through the namespace's TURN,
// exchange real RTP through the SFU (website/src/docs/developer/webrtc.mdx#architecture:
// iceTransportPolicy relay, TURN-shielded SFU).
func TestMedia_oneToOneRelayOnly(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	room := "e2e-1to1-" + fx.n.Name
	pub := joinPeer(t, fx, 0, room, true)
	sub := joinPeer(t, fx, 1, room, false)
	waitMedia(t, []*services.RTCPeer{pub}, []*services.RTCPeer{sub})
	if !pub.Connected() {
		t.Error("the publisher's ICE is not connected")
	}
}

// TestMedia_groupCall: three peers in one room on three gateways; two
// publish, and everyone hears someone.
func TestMedia_groupCall(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	room := "e2e-group-" + fx.n.Name
	a := joinPeer(t, fx, 0, room, true)
	b := joinPeer(t, fx, 1, room, true)
	c := joinPeer(t, fx, 2, room, false)
	waitMedia(t, []*services.RTCPeer{a, b}, []*services.RTCPeer{a, b, c})
}

// TestSignal_credentialsRefreshedAt80Percent: the SFU sends
// refresh-credentials at 80% of its 600 s TTL with a later expiry
// (website/src/docs/developer/webrtc.mdx#turn-credential-protocol).
func TestSignal_credentialsRefreshedAt80Percent(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	p, err := services.JoinRoom(t.Context(), fx.c, fx.token, "e2e-refresh-"+fx.n.Name, "refresher")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	joined := time.Now()
	if err := p.Start(false); err != nil {
		t.Fatal(err)
	}
	refresh := time.Duration(float64(sfuTTL) * refreshFraction)
	var got services.TURNCreds
	eventually.Require(t, time.Second, refresh+time.Minute, "refresh-credentials", func() (bool, error) {
		for {
			select {
			case m, ok := <-p.Messages:
				if !ok {
					return false, eventually.Stop(fmt.Errorf("the signalling socket closed"))
				}
				if m.Type == services.MsgRefreshCreds {
					return true, json.Unmarshal(m.Data, &got)
				}
			default:
				return false, nil
			}
		}
	})
	if took := time.Since(joined); took < refresh-time.Minute {
		t.Errorf("refreshed after %s, before 80%% of the TTL (%s)", took, refresh)
	}
	if got.Username <= p.Creds.Username || got.Password == p.Creds.Password {
		t.Errorf("the refreshed credential %q does not expire later than %q", got.Username, p.Creds.Username)
	}
}
