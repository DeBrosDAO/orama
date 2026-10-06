package wasmpolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	wasmvmtypes "github.com/CosmWasm/wasmvm/v3/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
)

type mapStore struct{ m map[string][]byte }

func newMapStore() *mapStore { return &mapStore{m: map[string][]byte{}} }

func (s *mapStore) Get(key []byte) []byte { return s.m[string(key)] }
func (s *mapStore) Set(key, value []byte) { s.m[string(key)] = append([]byte{}, value...) }
func (s *mapStore) Delete(key []byte)     { delete(s.m, string(key)) }
func (s *mapStore) Iterator(_, _ []byte) wasmvmtypes.Iterator {
	panic("not used")
}
func (s *mapStore) ReverseIterator(_, _ []byte) wasmvmtypes.Iterator {
	panic("not used")
}

func TestMeteredStore_countsNewKeysOverwritesAndDeletes(t *testing.T) {
	inner := newMapStore()
	ms := wasmpolicy.NewMeteredStore(inner)

	ms.Set([]byte("key"), []byte("12345"))
	grew, shrank := ms.Delta()
	require.Equal(t, uint64(8), grew, "a new entry weighs key plus value")
	require.Zero(t, shrank)

	ms.Set([]byte("key"), []byte("1234567"))
	grew, _ = ms.Delta()
	require.Equal(t, uint64(10), grew, "an overwrite adds only the longer value")

	ms.Set([]byte("key"), []byte("1"))
	_, shrank = ms.Delta()
	require.Equal(t, uint64(6), shrank, "an overwrite with a shorter value frees the difference")

	ms.Delete([]byte("key"))
	_, shrank = ms.Delta()
	require.Equal(t, uint64(6+4), shrank, "a delete frees key plus the stored value")
	require.Nil(t, inner.Get([]byte("key")))
}

func TestMeteredStore_missingKeyAndEmptyValues(t *testing.T) {
	ms := wasmpolicy.NewMeteredStore(newMapStore())

	ms.Delete([]byte("absent"))
	grew, shrank := ms.Delta()
	require.Zero(t, grew)
	require.Zero(t, shrank, "deleting a missing key frees nothing")

	ms.Set([]byte("k"), []byte{})
	grew, _ = ms.Delta()
	require.Equal(t, uint64(1), grew, "an empty value still costs its key")
}
