//go:build e2e_fleet

package webrtcchaos

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

const (
	// codeNamespaceGatewayUnavailable is the typed code of a signalling
	// upgrade that no member of the namespace could take.
	codeNamespaceGatewayUnavailable = "NAMESPACE_GATEWAY_UNAVAILABLE"
	gatewayUnitPrefix               = "orama-namespace-gateway@"
)

// TestSignalingSocket_failsOverWhenMemberGatewaysAreDown: a signalling socket
// entering at a node whose own namespace gateway is down is taken by another
// member's gateway, which routes the room to its SFU; with every member's
// gateway down the upgrade is answered with a typed, retryable 503, not plain
// text (website/src/docs/developer/webrtc.mdx#signaling-socket-failover).
func TestSignalingSocket_failsOverWhenMemberGatewaysAreDown(t *testing.T) {
	fx := setup(t)
	unit := gatewayUnitPrefix + fx.n.Name + ".service"
	for _, m := range fx.members {
		eventually.Require(t, pollEvery, readyBudget, "the namespace gateway on "+m.Name, func() (bool, error) {
			return fx.f.Unit(t, m, unit) == "active", nil
		})
	}
	room := "e2e-wsfailover-" + fx.n.Name
	entry := fx.members[0]

	fx.f.HoldDown(t, entry, unit)
	t.Run("one member down", func(t *testing.T) {
		p, err := services.JoinRoom(t.Context(), fx.c.PinTo(entry.PublicIP), fx.token, room, "failover")
		if err != nil {
			t.Fatalf("a socket entering at %s, whose gateway is down, was not taken by another member: %v", entry.Name, err)
		}
		defer p.Close()
	})

	for _, m := range fx.members[1:] {
		fx.f.HoldDown(t, m, unit)
	}
	t.Run("every member down", func(t *testing.T) {
		var status int
		var body []byte
		eventually.Require(t, pollEvery, readyBudget, "a typed 503 for the signalling upgrade", func() (bool, error) {
			path := services.SignalPath + "?" + url.Values{"room": {room}}.Encode()
			conn, resp, err := fx.c.PinTo(entry.PublicIP).DialWS(t.Context(), path, fx.token, nil)
			if err == nil {
				conn.Close()
				return false, nil // a gateway came back; keep waiting for the outage to be visible
			}
			if resp == nil {
				return false, err
			}
			status = resp.StatusCode
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			var env struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if status == http.StatusServiceUnavailable && json.Unmarshal(body, &env) == nil &&
				env.Error.Code == codeNamespaceGatewayUnavailable && env.Error.Retryable {
				return true, nil
			}
			return false, nil
		})
		if status != http.StatusServiceUnavailable {
			t.Errorf("status %d body %q", status, body)
		}
	})
}
