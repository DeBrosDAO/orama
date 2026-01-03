//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestPubSub_Presence(t *testing.T) {
	SkipIfMissingGateway(t)

	topic := GenerateTopic()
	memberID := "user123"
	memberMeta := map[string]interface{}{"name": "Alice"}

	// 1. Subscribe with presence
	client1, err := NewWSPubSubPresenceClient(t, topic, memberID, memberMeta)
	if err != nil {
		t.Fatalf("failed to create presence client: %v", err)
	}
	defer client1.Close()

	// Wait for join event
	msg, err := client1.ReceiveWithTimeout(5 * time.Second)
	if err != nil {
		t.Fatalf("did not receive join event: %v", err)
	}

	var event map[string]interface{}
	if err := json.Unmarshal(msg, &event); err != nil {
		t.Fatalf("failed to unmarshal event: %v", err)
	}

	if event["type"] != "presence.join" {
		t.Fatalf("expected presence.join event, got %v", event["type"])
	}

	if event["member_id"] != memberID {
		t.Fatalf("expected member_id %s, got %v", memberID, event["member_id"])
	}

	// 2. Query presence endpoint
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    fmt.Sprintf("%s/v1/pubsub/presence?topic=%s", GetGatewayURL(), topic),
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("presence query failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["count"] != float64(1) {
		t.Fatalf("expected count 1, got %v", resp["count"])
	}

	members := resp["members"].([]interface{})
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}

	member := members[0].(map[string]interface{})
	if member["member_id"] != memberID {
		t.Fatalf("expected member_id %s, got %v", memberID, member["member_id"])
	}

	// 3. Subscribe second member
	memberID2 := "user456"
	client2, err := NewWSPubSubPresenceClient(t, topic, memberID2, nil)
	if err != nil {
		t.Fatalf("failed to create second presence client: %v", err)
	}
	// We'll close client2 later to test leave event

	// Client1 should receive join event for Client2
	msg2, err := client1.ReceiveWithTimeout(5 * time.Second)
	if err != nil {
		t.Fatalf("client1 did not receive join event for client2: %v", err)
	}

	if err := json.Unmarshal(msg2, &event); err != nil {
		t.Fatalf("failed to unmarshal event: %v", err)
	}

	if event["type"] != "presence.join" || event["member_id"] != memberID2 {
		t.Fatalf("expected presence.join for %s, got %v for %v", memberID2, event["type"], event["member_id"])
	}

	// 4. Disconnect client2 and verify leave event
	client2.Close()

	msg3, err := client1.ReceiveWithTimeout(5 * time.Second)
	if err != nil {
		t.Fatalf("client1 did not receive leave event for client2: %v", err)
	}

	if err := json.Unmarshal(msg3, &event); err != nil {
		t.Fatalf("failed to unmarshal event: %v", err)
	}

	if event["type"] != "presence.leave" || event["member_id"] != memberID2 {
		t.Fatalf("expected presence.leave for %s, got %v for %v", memberID2, event["type"], event["member_id"])
	}
}

