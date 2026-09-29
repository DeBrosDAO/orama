package wasmpolicy

import (
	wasmvmtypes "github.com/CosmWasm/wasmvm/v3/types"
)

// MeteredStore wraps the KV store wasmvm gives a contract call and counts how many bytes the
// call adds to and removes from the contract's storage. A stored entry weighs len(key)+len(value).
// The count is read after the call returns. It does not touch a bank or a deposit ledger.
type MeteredStore struct {
	wasmvmtypes.KVStore
	grew   uint64
	shrank uint64
}

var _ wasmvmtypes.KVStore = (*MeteredStore)(nil)

// NewMeteredStore wraps inner.
func NewMeteredStore(inner wasmvmtypes.KVStore) *MeteredStore {
	return &MeteredStore{KVStore: inner}
}

// Set stores value under key and counts the change in stored bytes.
func (s *MeteredStore) Set(key, value []byte) {
	if old := s.KVStore.Get(key); old != nil {
		s.count(uint64(len(old)), uint64(len(value)))
	} else {
		s.grew += uint64(len(key)) + uint64(len(value))
	}
	s.KVStore.Set(key, value)
}

// Delete removes key and counts the freed bytes.
func (s *MeteredStore) Delete(key []byte) {
	if old := s.KVStore.Get(key); old != nil {
		s.shrank += uint64(len(key)) + uint64(len(old))
	}
	s.KVStore.Delete(key)
}

func (s *MeteredStore) count(oldValue, newValue uint64) {
	if newValue >= oldValue {
		s.grew += newValue - oldValue
		return
	}
	s.shrank += oldValue - newValue
}

// Delta returns the bytes added and the bytes removed since the store was wrapped.
func (s *MeteredStore) Delta() (grew, shrank uint64) { return s.grew, s.shrank }
