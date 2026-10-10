package chainfaucet

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

const testChainID = "orama-stagenet-9"

// recipientN is the Nth test account: a canonical address.
func recipientN(t testing.TB, n byte) string {
	t.Helper()
	addr, err := clusterreg.AccountAddressOf(bytes.Repeat([]byte{n}, 33))
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

// fakeChain is a chain node: it answers the reads a transaction needs and keeps the account's
// sequence, which moves only when a transaction is waited for, as a block does. It fails the test
// if two transactions are ever in flight together, which is how two drips would sign one sequence.
type fakeChain struct {
	t *testing.T

	mu         sync.Mutex
	sequence   uint64
	inFlight   int
	maxFlight  int
	signedSeqs []uint64
	hashes     []string
	order      []string

	// simErr fails a simulation; waitErr fails the wait for a block; accountErr the account read.
	simErr     error
	broadcast  error
	waitErr    error
	accountErr error
	// sim is called with the transaction bytes of each simulation, when set.
	sim func(tx []byte) error
	// hold, when set, blocks each wait for a block until it is closed.
	hold chan struct{}
}

func (f *fakeChain) Account(context.Context, string) (clusterreg.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clusterreg.Account{Number: 7, Sequence: f.sequence}, f.accountErr
}
func (f *fakeChain) LatestHeight(context.Context) (uint64, error) { return 100, nil }
func (f *fakeChain) BaseFee(context.Context) (string, error)      { return "1", nil }
func (f *fakeChain) SimulateGas(_ context.Context, tx []byte) (uint64, error) {
	if f.sim != nil {
		if err := f.sim(tx); err != nil {
			return 0, err
		}
	}
	return 100_000, f.simErr
}
func (f *fakeChain) Broadcast(_ context.Context, tx []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.broadcast != nil {
		return "", f.broadcast
	}
	f.inFlight++
	if f.inFlight > f.maxFlight {
		f.maxFlight = f.inFlight
	}
	f.signedSeqs = append(f.signedSeqs, f.sequence)
	hash := clusterreg.TxHash(tx)
	f.hashes = append(f.hashes, hash)
	return hash, nil
}
func (f *fakeChain) WaitIncluded(ctx context.Context, hash string) (int64, error) {
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
	if f.waitErr != nil {
		return 0, f.waitErr
	}
	f.sequence++
	return int64(1000 + f.sequence), nil
}

// fakeIDs answers a fixed chain id, or fails.
type fakeIDs struct {
	id  string
	err error
}

func (f fakeIDs) ChainID(context.Context) (string, error) { return f.id, f.err }

func newTestService(t *testing.T, chain onchain.Chain, ids ChainIDSource) (*Service, *Key) {
	t.Helper()
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc, err := New(ctx, key, chain, ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	return svc, key
}

func norama(n int64) *big.Int { return big.NewInt(n) }

func requireRefusal(t *testing.T, err error, kind Kind) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Kind != kind {
		t.Fatalf("err = %v, want a %s refusal", err, kind)
	}
	return r
}

func mustContain(t *testing.T, got string, fragments ...string) {
	t.Helper()
	for _, f := range fragments {
		if !strings.Contains(got, f) {
			t.Errorf("%q lacks %q", got, f)
		}
	}
}
