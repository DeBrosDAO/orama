package nullifier

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"testing"

	dbm "github.com/cosmos/cosmos-db"
)

const (
	// benchBlockNullifiers is the nullifiers one block commits: a block of shielded bundles at the
	// per-block action cap.
	benchBlockNullifiers = 100
	// benchImportChunk is how many records one Import call writes while the store is filled.
	benchImportChunk = 50_000
	// benchSmallStore and benchLargeStore are the store sizes measured. The large one takes minutes
	// to fill, so it runs only with EnvBenchLarge set.
	benchSmallStore = 1_000_000
	benchLargeStore = 10_000_000
	// EnvBenchLarge enables the 10M-entry benchmark (`make bench BENCH_LARGE=1`).
	EnvBenchLarge = "ORAMA_BENCH_LARGE"
)

// benchStore is a filled store that outlives the benchmark function's repeated calls (the testing
// package calls it again for every calibration round and every -count), so a store is filled once
// per size and per process.
type benchStore struct {
	store  *Store
	dir    string
	next   uint64
	height int64
}

var (
	benchMu     sync.Mutex
	benchStores = map[uint64]*benchStore{}
)

func TestMain(m *testing.M) {
	code := m.Run()
	for _, bs := range benchStores {
		_ = bs.store.Close()
		_ = os.RemoveAll(bs.dir)
	}
	os.Exit(code)
}

// benchNullifier is the i-th nullifier of the benchmark: SHA-256 of i, so the keys spread over the
// key space like real ones.
func benchNullifier(i uint64) [nodeLen]byte {
	return sha256.Sum256(binary.BigEndian.AppendUint64(nil, i))
}

// storeOfSize returns the shared store holding entries nullifiers, filling it on first use. It
// opens a Pebble database (the backend oramad uses) in a temp dir and imports the records.
func storeOfSize(b *testing.B, entries uint64) *benchStore {
	b.Helper()
	benchMu.Lock()
	defer benchMu.Unlock()
	if bs, ok := benchStores[entries]; ok {
		return bs
	}
	dir, err := os.MkdirTemp("", "nullifier-bench-")
	if err != nil {
		b.Fatal(err)
	}
	db, err := dbm.NewDB("nullifiers", dbm.PebbleDBBackend, dir)
	if err != nil {
		b.Fatal(err)
	}
	bs := &benchStore{store: NewStore(db), dir: dir, next: entries}
	benchStores[entries] = bs
	chunk := make([][nodeLen]byte, 0, benchImportChunk)
	for i := uint64(0); i < entries; i++ {
		chunk = append(chunk, benchNullifier(i))
		if len(chunk) == benchImportChunk || i == entries-1 {
			if err := bs.store.Import(chunk); err != nil {
				b.Fatal(err)
			}
			chunk = chunk[:0]
		}
	}
	return bs
}

// BenchmarkCommit is the cost of committing one block's nullifiers (benchBlockNullifiers of them)
// to a store already holding the named number of entries.
func BenchmarkCommit(b *testing.B) {
	sizes := []uint64{benchSmallStore}
	if os.Getenv(EnvBenchLarge) != "" {
		sizes = append(sizes, benchLargeStore)
	}
	for _, entries := range sizes {
		b.Run(fmt.Sprintf("entries=%d", entries), func(b *testing.B) {
			bs := storeOfSize(b, entries)
			block := make([][nodeLen]byte, benchBlockNullifiers)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				for j := range block {
					block[j] = benchNullifier(bs.next)
					bs.next++
				}
				bs.height++
				b.StartTimer()
				if err := bs.store.Commit(bs.height, block); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
