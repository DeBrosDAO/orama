package serverless

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

// wasmFakeIPFS embeds the basic MockIPFSClient and overrides Pin/Get so we can
// assert the replication factor, force a pin failure, and simulate a cold
// fetch that fails a few times before the block resolves (bugboard #137).
type wasmFakeIPFS struct {
	*MockIPFSClient
	pinErr        error
	pinReplFactor int
	pinCalls      int
	failCID       string   // simulate a pin failure for this specific CID (gone block)
	pinnedCIDs    []string // CIDs successfully pinned

	getErrN  int // return an error for the first N Get calls
	getCalls int
	getData  []byte
}

func (f *wasmFakeIPFS) Pin(ctx context.Context, cid, name string, replicationFactor int) (*ipfs.PinResponse, error) {
	f.pinCalls++
	f.pinReplFactor = replicationFactor
	if f.pinErr != nil {
		return nil, f.pinErr
	}
	if f.failCID != "" && cid == f.failCID {
		return nil, fmt.Errorf("simulated pin failure for gone block %s", cid)
	}
	f.pinnedCIDs = append(f.pinnedCIDs, cid)
	return &ipfs.PinResponse{Cid: cid, Name: name}, nil
}

func wasmFakeContains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func (f *wasmFakeIPFS) Get(ctx context.Context, cid, apiURL string) (io.ReadCloser, error) {
	f.getCalls++
	if f.getCalls <= f.getErrN {
		return nil, fmt.Errorf("simulated cold IPFS fetch failure (attempt %d)", f.getCalls)
	}
	return io.NopCloser(bytes.NewReader(f.getData)), nil
}

func newWASMTestRegistry(t *testing.T, ip ipfs.IPFSClient) *Registry {
	t.Helper()
	return NewRegistry(NewMockRQLite(), ip, RegistryConfig{IPFSAPIURL: "http://localhost:4501"}, zap.NewNop())
}

// TestUploadWASM_pinsEverywhere asserts a deploy pins the WASM on every cluster
// peer (replication factor -1), not RF=3 — so no gateway node is ever cold.
func TestUploadWASM_pinsEverywhere(t *testing.T) {
	ip := &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient()}
	registry := newWASMTestRegistry(t, ip)

	if _, err := registry.Register(context.Background(),
		&FunctionDefinition{Name: "fn-a", Namespace: "ns", IsPublic: true},
		[]byte("wasm-bytes")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if ip.pinCalls == 0 {
		t.Fatal("expected the WASM to be pinned on deploy")
	}
	if ip.pinReplFactor != wasmReplicationEverywhere {
		t.Errorf("pin replication factor = %d, want %d (pin-everywhere)", ip.pinReplFactor, wasmReplicationEverywhere)
	}
}

// TestUploadWASM_pinFailureFailsDeploy asserts a function whose WASM cannot be
// pinned is NOT silently "deployed" — the deploy must fail loud.
func TestUploadWASM_pinFailureFailsDeploy(t *testing.T) {
	ip := &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient(), pinErr: errors.New("cluster pin refused")}
	registry := newWASMTestRegistry(t, ip)

	_, err := registry.Register(context.Background(),
		&FunctionDefinition{Name: "fn-b", Namespace: "ns", IsPublic: true},
		[]byte("wasm-bytes"))
	if err == nil {
		t.Fatal("expected Register to fail when WASM pin fails, got nil")
	}
}

// TestGetWASMBytes_retriesColdFetch asserts a cold fetch that fails twice and
// then resolves returns the bytes (the per-attempt deadline + retry decouples
// the fetch from the function's execution budget).
func TestGetWASMBytes_retriesColdFetch(t *testing.T) {
	want := []byte("the-real-wasm")
	ip := &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient(), getErrN: 2, getData: want}
	registry := newWASMTestRegistry(t, ip)

	got, err := registry.GetWASMBytes(context.Background(), "QmColdCID")
	if err != nil {
		t.Fatalf("GetWASMBytes after retries: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if ip.getCalls != 3 {
		t.Errorf("Get called %d times, want 3 (2 failures + 1 success)", ip.getCalls)
	}
}

// TestGetWASMBytes_exhaustedReturnsTypedTimeout asserts that when every attempt
// fails, the typed ErrWASMFetchTimeout is returned (so the handler flags the
// invoke retryable instead of a permanent FUNCTION_EXECUTION_FAILED).
func TestGetWASMBytes_exhaustedReturnsTypedTimeout(t *testing.T) {
	ip := &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient(), getErrN: 99}
	registry := newWASMTestRegistry(t, ip)

	_, err := registry.GetWASMBytes(context.Background(), "QmNeverResolves")
	if err == nil {
		t.Fatal("expected an error when all fetch attempts fail")
	}
	if !errors.Is(err, ErrWASMFetchTimeout) {
		t.Errorf("error = %v, want it to wrap ErrWASMFetchTimeout", err)
	}
	if ip.getCalls != wasmFetchMaxAttempts {
		t.Errorf("Get attempted %d times, want %d", ip.getCalls, wasmFetchMaxAttempts)
	}
}

// TestRepinWASMCIDs_pinsAllEverywhereBestEffort asserts the GC-safety backfill
// pins every CID at replication=-1 and is best-effort: a gone (un-pinnable)
// block doesn't stop the surviving ones from being protected (incident
// 2026-06-24).
func TestRepinWASMCIDs_pinsAllEverywhereBestEffort(t *testing.T) {
	ip := &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient(), failCID: "QmGone"}
	registry := newWASMTestRegistry(t, ip)

	n := registry.repinWASMCIDs(context.Background(), []string{"QmA", "QmGone", "QmB"})

	if n != 2 {
		t.Errorf("pinned = %d, want 2 (QmGone fails, best-effort continues)", n)
	}
	if ip.pinReplFactor != wasmReplicationEverywhere {
		t.Errorf("replication factor = %d, want %d (pin-everywhere)", ip.pinReplFactor, wasmReplicationEverywhere)
	}
	if !wasmFakeContains(ip.pinnedCIDs, "QmA") || !wasmFakeContains(ip.pinnedCIDs, "QmB") {
		t.Errorf("expected QmA + QmB pinned, got %v", ip.pinnedCIDs)
	}
	if wasmFakeContains(ip.pinnedCIDs, "QmGone") {
		t.Errorf("QmGone should have failed to pin, got %v", ip.pinnedCIDs)
	}
}

// TestRepinWASMCIDs_empty is the trivial no-functions case.
func TestRepinWASMCIDs_empty(t *testing.T) {
	ip := &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient()}
	registry := newWASMTestRegistry(t, ip)
	if n := registry.repinWASMCIDs(context.Background(), nil); n != 0 {
		t.Errorf("pinned = %d, want 0", n)
	}
	if ip.pinCalls != 0 {
		t.Errorf("pin called %d times for empty input, want 0", ip.pinCalls)
	}
}

// TestGetWASMBytes_emptyCID validates the cheap boundary check.
func TestGetWASMBytes_emptyCID(t *testing.T) {
	registry := newWASMTestRegistry(t, &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient()})
	if _, err := registry.GetWASMBytes(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty wasmCID")
	}
}
