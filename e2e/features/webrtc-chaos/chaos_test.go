//go:build e2e_fleet

package webrtcchaos

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	pollEvery     = 5 * time.Second
	readyBudget   = 5 * time.Minute
	drainBudget   = time.Minute
	mediaBudget   = 2 * time.Minute
	cleanupBudget = 5 * time.Minute
	// reallocBudget: a member is non-viable after 10 minutes silent and its
	// row pruned after 15; the reconciler sweeps every 60 s
	// (docs/WEBRTC.md#role-reconciliation).
	reallocBudget = 25 * time.Minute
	// chainAdvance is how many blocks every co-hosted validator must commit
	// after the heal.
	chainAdvance = 2
)

type fixture struct {
	f     *fleet.Fleet
	n     *ns.Namespace
	c     *gw.Client
	token string
	// members are the nodes the namespace is placed on (a larger fleet has
	// others, which hold none of its units).
	members []fleet.Node
}

func setup(t *testing.T) *fixture {
	t.Helper()
	infra.RequireHealthy(t)
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	n.CLI.MustOK(t, "namespace", "enable", "webrtc", "--namespace", n.Name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		if res, err := n.CLI.Run(ctx, "namespace", "disable", "webrtc", "--namespace", n.Name); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: disabling WebRTC: %v %s", err, res.Stderr)
		}
	})
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name))
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	n.CLI.MustOK(t, "members", "add", w.Address(), "--role", "runtime")
	s, err := harness.GW(t).For(t).SignIn(t.Context(), w, n.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{f: f, n: n, c: c, token: s.AccessToken, members: tenancy.Members(t, f, n.Name)}
}

// TestSFUDown_drainAndReconnect: stopping the SFU that hosts a room tells its peers
// server-draining (or closes them), and a client that reconnects through
// another node's gateway gets media again (docs/WEBRTC.md#3-connect-signaling-websocket).
func TestSFUDown_drainAndReconnect(t *testing.T) {
	fx := setup(t)
	room := "e2e-drain-" + fx.n.Name
	unit := "orama-namespace-sfu@" + fx.n.Name + ".service"
	for _, m := range fx.members {
		eventually.Require(t, pollEvery, readyBudget, "the SFU on "+m.Name, func() (bool, error) {
			return fx.f.Unit(t, m, unit) == "active", nil
		})
	}
	// The room lives on the SFU its rendezvous rank puts first, whichever
	// gateway the join lands on: stopping any other member's SFU leaves the
	// participant untouched.
	victim := roomOwner(t, fx, room)
	p, err := services.JoinRoom(t.Context(), fx.c.PinTo(victim.PublicIP), fx.token, room, "drained")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Start(false); err != nil {
		t.Fatal(err)
	}
	t.Run("sfu stopped", func(t *testing.T) {
		fx.f.StopService(t, victim, unit)
		eventually.Require(t, time.Second, drainBudget, "server-draining or a close", func() (bool, error) {
			select {
			case m, ok := <-p.Messages:
				return !ok || m.Type == services.MsgServerDraining, nil
			default:
				return false, nil
			}
		})
		requireMediaThroughOthers(t, fx, room, victim)
	})
}

// roomOwner is the member whose SFU hosts room when every SFU is healthy and
// the room is new: the top of the rendezvous rank every gateway computes,
// sha256 of namespace|room|node id, the id being the node's dns_nodes id
// (docs/WEBRTC.md "Room Placement"; core pkg/gateway/handlers/webrtc
// rankSFUNodes).
func roomOwner(t *testing.T, fx *fixture, room string) fleet.Node {
	t.Helper()
	var owner fleet.Node
	var ownerID string
	var best uint64
	for i, m := range fx.members {
		q := infra.IndexQuery(t, fx.f, m, "SELECT id FROM dns_nodes WHERE internal_ip = ?", m.WGIP)
		if len(q.Values) != 1 {
			t.Fatalf("dns_nodes has %d rows for %s (%s)", len(q.Values), m.Name, m.WGIP)
		}
		id, ok := q.Values[0][0].(string)
		if !ok || id == "" {
			t.Fatalf("dns_nodes id of %s is %v: the room's owner cannot be computed", m.Name, q.Values[0][0])
		}
		sum := sha256.Sum256([]byte(fx.n.Name + "\x00" + room + "\x00" + id))
		score := binary.BigEndian.Uint64(sum[:8])
		if i == 0 || score > best || (score == best && id < ownerID) {
			owner, ownerID, best = m, id, score
		}
	}
	return owner
}

// requireMediaThroughOthers rejoins room through two members other than
// stopped (publisher and subscriber) and waits for media to flow between them.
func requireMediaThroughOthers(t *testing.T, fx *fixture, room string, stopped fleet.Node) {
	t.Helper()
	var rest []fleet.Node
	for _, m := range fx.members {
		if m.Name != stopped.Name {
			rest = append(rest, m)
		}
	}
	if len(rest) < 2 {
		t.Fatalf("%d members besides %s: need a publisher and a subscriber", len(rest), stopped.Name)
	}
	pubNode, subNode := rest[0], rest[1]
	pub, err := services.JoinRoom(t.Context(), fx.c.PinTo(pubNode.PublicIP), fx.token, room, "rejoined-pub")
	if err != nil {
		t.Fatalf("reconnecting through %s: %v", pubNode.Name, err)
	}
	defer pub.Close()
	sub, err := services.JoinRoom(t.Context(), fx.c.PinTo(subNode.PublicIP), fx.token, room, "rejoined-sub")
	if err != nil {
		t.Fatalf("reconnecting through %s: %v", subNode.Name, err)
	}
	defer sub.Close()
	if err := pub.Start(true); err != nil {
		t.Fatal(err)
	}
	if err := sub.Start(false); err != nil {
		t.Fatal(err)
	}
	eventually.Require(t, time.Second, mediaBudget, "media after the reconnect", func() (bool, error) {
		if err := pub.Publish(); err != nil {
			return false, err
		}
		return sub.Received() > 0, nil
	})
}

// TestNodeDeath_rolesReallocated: a TURN holder cut off from the other two for
// longer than the viability grace loses its roles: TURN DNS for the namespace
// stops naming it and names only live nodes, which serve relays; when the
// partition heals the cluster converges again. Two of the namespace's three
// members hold TURN, so the victim is whichever of them does that is not the
// resolver (the first nameserver): cutting off a node that never held TURN
// would prove nothing.
func TestNodeDeath_rolesReallocated(t *testing.T) {
	fx := setup(t)
	host := "turn.ns-" + fx.n.Name + "." + fx.f.State.BaseDomain
	resolver := tenancy.Nameservers(fx.f)[0]
	var dead fleet.Node
	eventually.Require(t, pollEvery, readyBudget, "TURN DNS for "+host+" to name two nodes", func() (bool, error) {
		got, err := tenancy.ResolveAt(t.Context(), resolver.PublicIP, host)
		if len(got) != 2 {
			return false, fmt.Errorf("%v %v", got, err)
		}
		for _, n := range fx.members {
			if n.Name != resolver.Name && slices.Contains(got, n.PublicIP) {
				dead = n
				return true, nil
			}
		}
		return false, eventually.Stop(fmt.Errorf("no member besides the resolver %s holds TURN: %v", resolver.Name, got))
	})
	t.Run(dead.Name+" partitioned", func(t *testing.T) {
		for _, peer := range fx.f.State.Nodes {
			if peer.Name != dead.Name {
				fx.f.IPTablesBlock(t, dead, peer)
			}
		}
		eventually.Require(t, time.Minute, reallocBudget, "TURN roles off "+dead.Name, func() (bool, error) {
			got, err := tenancy.ResolveAt(t.Context(), resolver.PublicIP, host)
			if err != nil {
				return false, err
			}
			if len(got) > 0 && !slices.Contains(got, dead.PublicIP) {
				return true, nil
			}
			return false, fmt.Errorf("TURN DNS %v", got)
		})
		r := fx.c.PinTo(survivor(fx, dead).PublicIP).MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/webrtc/turn/credentials", Bearer: fx.token})
		var cr services.TURNCreds
		if err := r.Expect(t, http.StatusOK).Decode(&cr); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), services.GatherBudget)
		defer cancel()
		cands, err := services.RelayCandidates(ctx, cr)
		if err != nil || len(cands) == 0 {
			t.Errorf("no relay from the reallocated TURN: %v %v", cands, err)
		}
	})
	infra.WaitConverged(t, len(fx.f.State.Nodes), reallocBudget, "the cluster after the partition heals")
	requireChainAdvances(t, fx.f)
}

// survivor is a member of the namespace other than dead.
func survivor(fx *fixture, dead fleet.Node) fleet.Node {
	for _, n := range fx.members {
		if n.Name != dead.Name {
			return n
		}
	}
	panic("the namespace has no member besides " + dead.Name)
}

// requireChainAdvances: when the run co-hosts a chain, cutting one of its
// three equal validators off halts it (exactly 2/3 is no CometBFT quorum,
// docs/CHAIN.md "x/power"), so after the heal every validator must commit
// chainAdvance blocks past the head again before the next package runs.
func requireChainAdvances(t *testing.T, f *fleet.Fleet) {
	t.Helper()
	if f.State.ChainID == "" {
		return
	}
	c := chain.New(t)
	c.WaitHeight(t, c.Height(t)+chainAdvance)
}
