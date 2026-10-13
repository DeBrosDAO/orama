package sfu

import (
	"encoding/json"
	"testing"
)

func readType(t *testing.T, conn interface{ ReadJSON(any) error }, want MessageType) rawFrame {
	t.Helper()
	for i := 0; i < 20; i++ {
		var m rawFrame
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("waiting for %s: %v", want, err)
		}
		if m.Type == want {
			return m
		}
	}
	t.Fatalf("no %s frame arrived", want)
	return rawFrame{}
}

func sendState(t *testing.T, conn interface{ WriteJSON(any) error }, kind MessageType, data string) {
	t.Helper()
	if err := conn.WriteJSON(ClientMessage{Type: kind, Data: json.RawMessage(data)}); err != nil {
		t.Fatal(err)
	}
}

func TestSignal_audioAndVideoStateAreRelayedToTheRoom(t *testing.T) {
	s := newAuthServer(t)
	alice, _ := joinAs(t, s, testTicket(t, s, "r1", "alice"), "")
	bob, _ := joinAs(t, s, testTicket(t, s, "r1", "bob"), "")

	sendState(t, alice, MessageTypeAudioState, `{"enabled":false}`)
	sendState(t, alice, MessageTypeVideoState, `{"enabled":true}`)

	for _, want := range []struct {
		kind    string
		enabled bool
	}{{"audio", false}, {"video", true}} {
		m := readType(t, bob, MessageTypeParticipantState)
		var got ParticipantStateData
		if err := json.Unmarshal(m.Data, &got); err != nil {
			t.Fatal(err)
		}
		if got.UserID != "alice" || got.Kind != want.kind || got.Enabled != want.enabled || got.Forced {
			t.Fatalf("state = %+v, want alice's %s enabled=%v, not forced", got, want.kind, want.enabled)
		}
	}
}

func TestSignal_stateWithoutEnabledIsAnError(t *testing.T) {
	s := newAuthServer(t)
	alice, _ := joinAs(t, s, testTicket(t, s, "r1", "alice"), "")

	for _, data := range []string{`{}`, `{"enabled":"yes"}`, `[1]`} {
		sendState(t, alice, MessageTypeAudioState, data)
		m := readType(t, alice, MessageTypeError)
		var e ErrorData
		_ = json.Unmarshal(m.Data, &e)
		if e.Code != "invalid_state" {
			t.Fatalf("data %s: error = %+v, want invalid_state (and never unknown_message)", data, e)
		}
	}
}

func TestSignal_stateIsNotEchoedToTheSender(t *testing.T) {
	s := newAuthServer(t)
	alice, _ := joinAs(t, s, testTicket(t, s, "r1", "alice"), "")
	bob, _ := joinAs(t, s, testTicket(t, s, "r1", "bob"), "")

	sendState(t, alice, MessageTypeAudioState, `{"enabled":true}`)
	readType(t, bob, MessageTypeParticipantState)
	sendState(t, alice, MessageTypeAudioState, `{}`) // an error comes back to alice, nothing else
	if m := readType(t, alice, MessageTypeError); m.Type != MessageTypeError {
		t.Fatal("alice did not get her own error")
	}
}
