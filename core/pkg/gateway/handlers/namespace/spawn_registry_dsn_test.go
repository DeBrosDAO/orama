package namespace

import (
	"strings"
	"testing"

	"go.uber.org/zap"
)

const (
	localRegistryDSN     = "http://10.0.0.2:10100"
	requesterRegistryDSN = "http://10.0.0.1:10100"
)

func TestGatewayRegistryDSN_usesThisNodesRegistryNotTheRequesters(t *testing.T) {
	h := NewSpawnHandler(nil, "unused", "node-2", zap.NewNop())
	h.SetRegistryDSN(localRegistryDSN)
	got, err := h.gatewayRegistryDSN(SpawnRequest{Namespace: "ns", GatewayGlobalRQLiteDSN: requesterRegistryDSN})
	if err != nil {
		t.Fatal(err)
	}
	if got != localRegistryDSN {
		t.Fatalf("registry DSN = %q, want this node's %q", got, localRegistryDSN)
	}
}

func TestGatewayRegistryDSN_indexGatewayStaysWithoutOne(t *testing.T) {
	h := NewSpawnHandler(nil, "unused", "node-2", zap.NewNop())
	h.SetRegistryDSN(localRegistryDSN)
	got, err := h.gatewayRegistryDSN(SpawnRequest{Namespace: "index"})
	if err != nil || got != "" {
		t.Fatalf("got %q, %v; want empty and no error", got, err)
	}
}

func TestGatewayRegistryDSN_errorsWhenNodeHasNoRegistryAddress(t *testing.T) {
	h := NewSpawnHandler(nil, "unused", "node-2", zap.NewNop())
	_, err := h.gatewayRegistryDSN(SpawnRequest{Namespace: "ns", GatewayGlobalRQLiteDSN: requesterRegistryDSN})
	if err == nil || !strings.Contains(err.Error(), "node-2") {
		t.Fatalf("err = %v, want one naming the node", err)
	}
}
