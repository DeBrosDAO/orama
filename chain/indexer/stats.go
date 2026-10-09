package indexer

import (
	"fmt"
	"time"

	"cosmossdk.io/math"
)

// HourlyWindow is how many hourly buckets Stats returns: 48, so a reader can
// compare the last 24 hours with the 24 before.
const HourlyWindow = 48

// hourOf is the unix hour a time falls in.
func hourOf(t time.Time) int64 { return t.UTC().Unix() / int64(time.Hour/time.Second) }

type hourRecord struct {
	Txs    uint64 `json:"txs"`
	Failed uint64 `json:"failed"`
	Burned string `json:"burned"`
}

// addToHour counts one transaction in the hour of its block.
func (w *writer) addToHour(blockTime time.Time, failed bool, burned math.Int) error {
	key := hourKey(hourOf(blockTime))
	rec := hourRecord{Burned: "0"}
	if _, err := getJSON(w.b, key, &rec); err != nil {
		return err
	}
	have, ok := math.NewIntFromString(rec.Burned)
	if !ok {
		return fmt.Errorf("hour record %q holds burned %q, not an integer", key, rec.Burned)
	}
	rec.Txs++
	if failed {
		rec.Failed++
	}
	rec.Burned = have.Add(burned).String()
	return w.setJSON(key, rec)
}

// touchAccount counts one transaction that names addr at the block's time.
func (w *writer) touchAccount(addr string, blockTime time.Time) error {
	key := summaryKey(addr)
	var sum AccountSummary
	ok, err := getJSON(w.b, key, &sum)
	if err != nil {
		return err
	}
	if !ok {
		sum = AccountSummary{Address: addr, FirstSeen: blockTime}
	}
	sum.TxCount++
	sum.LastActive = blockTime
	return w.setJSON(key, sum)
}

func (w *writer) addLatest(height int64, index uint32, hash []byte) error {
	return w.set(latestKey(height, index), hash)
}

// LatestTxs returns the newest transactions, newest first.
func (s *Store) LatestTxs(limit int) ([]Tx, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	rows, err := scan(snap, []byte(pfxLatest), true, 1, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Tx, 0, len(rows))
	for _, row := range rows {
		var t Tx
		ok, err := getJSON(snap, txKey(row.value), &t)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("latest-transaction index names %x but the index does not hold it", row.value)
		}
		out = append(out, t)
	}
	return out, nil
}

// Account returns what the index knows of addr.
func (s *Store) Account(addr string) (AccountSummary, bool, error) {
	var sum AccountSummary
	ok, err := getJSON(s.db, summaryKey(addr), &sum)
	return sum, ok, err
}

// Stats returns the hourly buckets of the HourlyWindow hours ending with the
// hour of the newest indexed block, oldest first. An hour with no
// transaction is a bucket of zeros, so the series has no holes. It is empty on
// an index with no block.
func (s *Store) Stats() ([]HourStat, error) {
	st, err := s.Status()
	if err != nil {
		return nil, err
	}
	if st.Cursor == 0 {
		return []HourStat{}, nil
	}
	head, ok, err := s.Block(st.Cursor)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("index cursor is block %d but the index does not hold it", st.Cursor)
		}
		return nil, err
	}
	last := hourOf(head.Time)
	first := last - HourlyWindow + 1
	out := make([]HourStat, 0, HourlyWindow)
	for h := first; h <= last; h++ {
		rec := hourRecord{Burned: "0"}
		if _, err := getJSON(s.db, hourKey(h), &rec); err != nil {
			return nil, err
		}
		out = append(out, HourStat{
			Hour: time.Unix(h*int64(time.Hour/time.Second), 0).UTC(),
			Txs:  rec.Txs, Failed: rec.Failed, Burned: rec.Burned,
		})
	}
	return out, nil
}
