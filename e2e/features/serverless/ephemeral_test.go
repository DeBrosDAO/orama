//go:build e2e_fleet

package serverless

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Ephemeral state limits (docs/SERVERLESS.md#ephemeral-state-ws-subscribe-tracked;
// core/pkg/serverless/ephemeral_state.go).
const (
	ephMaxPayload = 16 << 10
	ephMaxKeys    = 256
	ephShortTTLms = 2000
	frameBudget   = 30 * time.Second
	clearBudget   = 30 * time.Second
)

// wsCall sends one frame on a stateless function socket and returns the
// function's output from the reply.
func wsCall(t *testing.T, conn *websocket.Conn, body map[string]any) map[string]any {
	t.Helper()
	if err := conn.WriteJSON(body); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(frameBudget)); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Output map[string]any `json:"output"`
		Error  string         `json:"error"`
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("reading the reply to %v: %v", body["op"], err)
	}
	if err := json.Unmarshal(msg, &reply); err != nil || reply.Error != "" {
		t.Fatalf("reply to %v: %v %s", body["op"], err, msg)
	}
	return reply.Output
}

// entries lists the live entries on topic over HTTP (list works without a
// socket) through gateway c.
func entries(t *testing.T, fx *fixture, c *gw.Client, fn, topic string) []any {
	t.Helper()
	res := invoke(t, c, fn, fx.admin, map[string]any{"op": "eph_list", "topic": topic})
	var out map[string]any
	if err := res.Decode(&out); err != nil {
		t.Fatal(err)
	}
	e, _ := sub(out, "result")["entries"].([]any)
	return e
}

// TestEphemeral_limitsAndDisconnect: state needs a socket; a payload over
// 16 KiB and a 257th key are refused; a short TTL expires; a disconnect
// clears what the socket owned.
func TestEphemeral_limitsAndDisconnect(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const fn = "e2e-eph"
	deploy(t, fx, fnSpec{name: fn})
	c := fx.c.PinTo(fx.f.State.Nodes[0].PublicIP)
	topic := "typing:" + fx.n.Name
	if got := call(t, fx, fn, map[string]any{"op": "eph_set", "topic": topic, "key": "k"})["ok"]; got != float64(0) {
		t.Errorf("ephemeral_state_set without a socket returned %v, want 0", got)
	}
	conn, _, err := c.DialWS(t.Context(), "/v1/functions/"+fn+"/ws", fx.admin, nil)
	if err != nil {
		t.Fatalf("opening the function socket: %v", err)
	}
	defer conn.Close()
	checks := map[string]struct {
		body map[string]any
		want float64
	}{
		"16 KiB payload":   {map[string]any{"op": "eph_set", "topic": topic, "key": "max", "size": ephMaxPayload}, 1},
		"over 16 KiB":      {map[string]any{"op": "eph_set", "topic": topic, "key": "big", "size": ephMaxPayload + 1}, 0},
		"empty key":        {map[string]any{"op": "eph_set", "topic": topic, "key": ""}, 0},
		"empty topic":      {map[string]any{"op": "eph_set", "topic": "", "key": "k"}, 0},
		"clear missing":    {map[string]any{"op": "eph_clear", "topic": topic, "key": "nope"}, 1},
		"unicode key/data": {map[string]any{"op": "eph_set", "topic": topic, "key": "ユーザー‮", "payload": "\u0000ü"}, 1},
	}
	for name, check := range checks {
		if got := wsCall(t, conn, check.body)["ok"]; got != check.want {
			t.Errorf("%s: %v, want %v", name, got, check.want)
		}
	}
	if got := wsCall(t, conn, map[string]any{"op": "eph_set", "topic": topic, "key": "many", "count": ephMaxKeys + 1})["ok"]; got == float64(ephMaxKeys+1) {
		t.Errorf("a socket set %d keys; the cap is %d", ephMaxKeys+1, ephMaxKeys)
	}
	if n := len(entries(t, fx, c, fn, topic)); n == 0 {
		t.Fatal("no live entries while the socket is open")
	}
	conn.Close()
	eventually.Require(t, time.Second, clearBudget, "disconnect to clear the socket's state", func() (bool, error) {
		n := len(entries(t, fx, c, fn, topic))
		if n == 0 {
			return true, nil
		}
		return false, fmt.Errorf("%d entries remain", n)
	})
}

// TestEphemeral_ttlBackstop: an entry set with a short TTL expires without a
// disconnect or a clear.
func TestEphemeral_ttlBackstop(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const fn = "e2e-eph-ttl"
	deploy(t, fx, fnSpec{name: fn})
	c := fx.c.PinTo(fx.f.State.Nodes[1].PublicIP)
	topic := "presence:" + fx.n.Name
	conn, _, err := c.DialWS(t.Context(), "/v1/functions/"+fn+"/ws", fx.admin, nil)
	if err != nil {
		t.Fatalf("opening the function socket: %v", err)
	}
	defer conn.Close()
	if got := wsCall(t, conn, map[string]any{"op": "eph_set", "topic": topic, "key": "short", "ttl": ephShortTTLms})["ok"]; got != float64(1) {
		t.Fatalf("set: %v", got)
	}
	eventually.Require(t, time.Second, clearBudget, "a 2s entry to expire", func() (bool, error) {
		return len(entries(t, fx, c, fn, topic)) == 0, nil
	})
	if got := wsCall(t, conn, map[string]any{"op": "whoami"})["ws_client"]; got == "" || got == nil {
		t.Errorf("get_ws_client_id is empty on a socket invocation")
	}
}
