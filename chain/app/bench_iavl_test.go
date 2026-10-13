package app_test

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/iavl"
	iavldb "github.com/cosmos/iavl/db"
)

const (
	// envBenchLarge adds the 10M-key IAVL size (`make bench BENCH_LARGE=1`). It is the same switch
	// as the nullifier store's 10M benchmark.
	envBenchLarge = "ORAMA_BENCH_LARGE"
	// iavlNodeCache is the SDK's default IAVL node cache size (store/iavl, DefaultIAVLCacheSize).
	iavlNodeCache = 781_250
	// iavlFillChunk is how many keys go into one saved version while a tree is filled.
	iavlFillChunk = 100_000
	// iavlBlockWrites is the keys one busy block writes: iavlBlockUpdates rewrite existing keys and
	// the rest are new.
	iavlBlockWrites  = 1_000
	iavlBlockUpdates = 800
	// iavlKeysSmall, iavlKeysMedium and iavlKeysLarge are the tree sizes measured; the large one
	// takes minutes to fill and needs envBenchLarge.
	iavlKeysSmall  = 100_000
	iavlKeysMedium = 1_000_000
	iavlKeysLarge  = 10_000_000
)

// iavlBench is a filled tree shared by the repeated calls the testing package makes.
type iavlBench struct {
	tree *iavl.MutableTree
	db   iavldb.DB
	dir  string
	keys uint64 // keys written so far; new keys continue from here
	seed uint64 // advances every block so updates touch different keys
}

var (
	iavlMu    sync.Mutex
	iavlTrees = map[uint64]*iavlBench{}
)

func TestMain(m *testing.M) {
	code := m.Run()
	for _, t := range iavlTrees {
		_ = t.db.Close()
		_ = os.RemoveAll(t.dir)
	}
	os.Exit(code)
}

// iavlKey is the i-th key: SHA-256 of i, spread over the key space like account and store keys.
func iavlKey(i uint64) []byte {
	k := sha256.Sum256(binary.BigEndian.AppendUint64(nil, i))
	return k[:]
}

// treeOfSize returns the shared tree holding n keys on a Pebble database, filling it on first use.
func treeOfSize(b *testing.B, n uint64) *iavlBench {
	b.Helper()
	iavlMu.Lock()
	defer iavlMu.Unlock()
	if t, ok := iavlTrees[n]; ok {
		return t
	}
	dir, err := os.MkdirTemp("", "iavl-bench-")
	if err != nil {
		b.Fatal(err)
	}
	db, err := iavldb.NewDB("iavl", string(dbm.PebbleDBBackend), dir)
	if err != nil {
		b.Fatal(err)
	}
	t := &iavlBench{tree: iavl.NewMutableTree(db, iavlNodeCache, false, iavl.NewNopLogger()), db: db, dir: dir}
	iavlTrees[n] = t
	value := make([]byte, 8)
	for t.keys < n {
		if _, err := t.tree.Set(iavlKey(t.keys), value); err != nil {
			b.Fatal(err)
		}
		t.keys++
		if t.keys%iavlFillChunk == 0 || t.keys == n {
			if _, _, err := t.tree.SaveVersion(); err != nil {
				b.Fatal(err)
			}
		}
	}
	return t
}

// BenchmarkIAVLCommit is writing one busy block (iavlBlockWrites keys, most of them updates) to an
// IAVL tree of the named size on Pebble and saving the version.
func BenchmarkIAVLCommit(b *testing.B) {
	sizes := []uint64{iavlKeysSmall, iavlKeysMedium}
	if os.Getenv(envBenchLarge) != "" {
		sizes = append(sizes, iavlKeysLarge)
	}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("keys=%d", n), func(b *testing.B) {
			t := treeOfSize(b, n)
			value := make([]byte, 8)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				t.seed++
				b.StartTimer()
				for w := 0; w < iavlBlockWrites; w++ {
					key := t.keys
					if w < iavlBlockUpdates {
						key = (t.seed*iavlBlockWrites + uint64(w)) % n
					} else {
						t.keys++
					}
					binary.BigEndian.PutUint64(value, t.seed)
					if _, err := t.tree.Set(iavlKey(key), value); err != nil {
						b.Fatal(err)
					}
				}
				if _, _, err := t.tree.SaveVersion(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
