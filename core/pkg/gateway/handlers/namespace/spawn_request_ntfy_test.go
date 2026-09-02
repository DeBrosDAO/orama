package namespace

import (
	"encoding/json"
	"testing"
)

// TestSpawnRequest_ntfyBaseURLWireKey pins the JSON key the cluster manager
// sends for the host ntfy base URL (bugboard #274).
//
// This link is easy to break silently and expensive to debug. The local node
// spawns its namespace gateway through InstanceConfig directly, but every
// REMOTE node receives its config as this JSON payload. If the key here and
// the key in ClusterManager's spawn payload ever drift, the local node gets a
// working ntfy provider and the remote nodes get none — Android push then
// succeeds or fails depending on which node served the request, which is the
// intermittent, per-node failure mode the anchat team specifically asked us to
// rule out.
func TestSpawnRequest_ntfyBaseURLWireKey(t *testing.T) {
	const base = "https://push.orama-devnet.network"
	payload := []byte(`{
		"action": "spawn-gateway",
		"namespace": "anchat-v2",
		"node_id": "node-1",
		"gateway_ntfy_base_url": "` + base + `"
	}`)

	var req SpawnRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.GatewayNtfyBaseURL != base {
		t.Errorf("GatewayNtfyBaseURL = %q; want %q — the wire key must stay \"gateway_ntfy_base_url\" to match ClusterManager's spawn payload", req.GatewayNtfyBaseURL, base)
	}
}

// TestSpawnRequest_ntfyBaseURLOmittedIsEmpty covers the mixed-version case
// during a rolling upgrade: an older node sends a payload with no ntfy key at
// all. That must decode cleanly to an empty string rather than erroring, so the
// spawn still succeeds (just without a platform ntfy default).
func TestSpawnRequest_ntfyBaseURLOmittedIsEmpty(t *testing.T) {
	payload := []byte(`{"action":"spawn-gateway","namespace":"anchat-v2","node_id":"node-1"}`)

	var req SpawnRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.GatewayNtfyBaseURL != "" {
		t.Errorf("GatewayNtfyBaseURL = %q; want empty", req.GatewayNtfyBaseURL)
	}
}
