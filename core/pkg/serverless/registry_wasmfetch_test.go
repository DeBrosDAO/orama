package serverless

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

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

	// PinStatus control (bugboard #137 pin verification).
	pinStatus      string // aggregated status to report; "" => "pinned"
	pinnedPeers    int    // PinnedPeers reported (defaults derived from pinStatus)
	totalPeers     int    // TotalPeers reported; 0 => 1
	pinStatusCalls int
	// pinnedAfterN: once PinStatus has been polled at least this many times,
	// report "pinned" (simulates async convergence). 0 disables (use pinStatus).
	pinnedAfterN int
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

func (f *wasmFakeIPFS) PinStatus(ctx context.Context, cid string) (*ipfs.PinStatus, error) {
	f.pinStatusCalls++

	status := f.pinStatus
	if status == "" {
		status = ipfs.PinStatusPinned
	}
	if f.pinnedAfterN > 0 && f.pinStatusCalls >= f.pinnedAfterN {
		status = ipfs.PinStatusPinned
	}

	total := f.totalPeers
	if total == 0 {
		total = 1
	}
	pinned := f.pinnedPeers
	if status == ipfs.PinStatusPinned {
		pinned = total
	}

	return &ipfs.PinStatus{
		Cid:         cid,
		Status:      status,
		PinnedPeers: pinned,
		TotalPeers:  total,
		Peers:       make([]string, total),
	}, nil
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
	// wasmFetchMaxAttempts local fetches + 1 post-repin recovery fetch, all of
	// which fail here (block gone everywhere), so the typed timeout still wins.
	if ip.getCalls != wasmFetchMaxAttempts+1 {
		t.Errorf("Get attempted %d times, want %d (local attempts + 1 recovery fetch)", ip.getCalls, wasmFetchMaxAttempts+1)
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

// withFastPinVerify shortens the pin-verification timings for a test and
// restores them afterwards, so the timeout path doesn't take 30s.
func withFastPinVerify(t *testing.T) {
	t.Helper()
	timeout, interval := wasmPinVerifyTimeout, wasmPinVerifyInterval
	wasmPinVerifyTimeout = 100 * time.Millisecond
	wasmPinVerifyInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		wasmPinVerifyTimeout = timeout
		wasmPinVerifyInterval = interval
	})
}

// TestUploadWASM_hardFailsWhenPinNeverConfirms asserts a deploy HARD-FAILS when
// the cluster pin is accepted but never converges to "pinned" on every peer —
// the exact "deployed but unfetchable" state that caused bugboard #137.
func TestUploadWASM_hardFailsWhenPinNeverConfirms(t *testing.T) {
	withFastPinVerify(t)
	ip := &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient(), pinStatus: ipfs.PinStatusPinning}
	registry := newWASMTestRegistry(t, ip)

	_, err := registry.Register(context.Background(),
		&FunctionDefinition{Name: "fn-stuck", Namespace: "ns", IsPublic: true},
		[]byte("wasm-bytes"))
	if err == nil {
		t.Fatal("expected Register to fail when pin never confirms 'pinned', got nil")
	}
	if ip.pinStatusCalls == 0 {
		t.Error("expected PinStatus to be polled during verification")
	}
}

// TestUploadWASM_succeedsWhenPinnedEverywhere asserts a deploy succeeds once the
// cluster reports the WASM pinned on every peer.
func TestUploadWASM_succeedsWhenPinnedEverywhere(t *testing.T) {
	ip := &wasmFakeIPFS{MockIPFSClient: NewMockIPFSClient(), pinStatus: ipfs.PinStatusPinned}
	registry := newWASMTestRegistry(t, ip)

	if _, err := registry.Register(context.Background(),
		&FunctionDefinition{Name: "fn-ok", Namespace: "ns", IsPublic: true},
		[]byte("wasm-bytes")); err != nil {
		t.Fatalf("Register with pinned WASM should succeed: %v", err)
	}
	if ip.pinStatusCalls == 0 {
		t.Error("expected PinStatus to be polled to confirm the pin")
	}
}

// TestGetWASMBytes_recoversViaRepin asserts a cold read miss (all local fetches
// time out) triggers ONE re-pin recovery and then serves the block on the final
// post-repin fetch (bugboard #137 peer-recoverable read).
func TestGetWASMBytes_recoversViaRepin(t *testing.T) {
	want := []byte("recovered-wasm")
	// getErrN == wasmFetchMaxAttempts: all local attempts fail, then the single
	// post-repin recovery fetch (call #4) succeeds.
	ip := &wasmFakeIPFS{
		MockIPFSClient: NewMockIPFSClient(),
		getErrN:        wasmFetchMaxAttempts,
		getData:        want,
		pinStatus:      ipfs.PinStatusPinned,
		pinnedPeers:    1,
		totalPeers:     1,
	}
	registry := newWASMTestRegistry(t, ip)

	got, err := registry.GetWASMBytes(context.Background(), "QmColdRecover")
	if err != nil {
		t.Fatalf("GetWASMBytes should recover via re-pin: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if ip.pinCalls == 0 {
		t.Error("expected a recovery re-pin to be issued on cold miss")
	}
	if ip.getCalls != wasmFetchMaxAttempts+1 {
		t.Errorf("Get called %d times, want %d (local attempts + 1 recovery fetch)", ip.getCalls, wasmFetchMaxAttempts+1)
	}
}
