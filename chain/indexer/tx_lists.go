package indexer

import "fmt"

// The transaction lists are newest first and are read straight off an index per filter, so a page
// costs the page and the rows skipped to reach it, not the transactions that do not match.

func kindTxPrefix(kind string) []byte { return []byte(pfxKindTx + kind + "/") }
func kindTxKey(kind string, height int64, index uint32) []byte {
	return join(kindTxPrefix(kind), be64(uint64(height)), be32(index))
}
func kindAcctPrefix(addr, kind string) []byte { return []byte(pfxKindAcct + addr + "/" + kind + "/") }
func kindAcctKey(addr, kind string, height int64, index uint32) []byte {
	return join(kindAcctPrefix(addr, kind), be64(uint64(height)), be32(index))
}

// addKinds lists the transaction under each filter it matches (besides "all", which the latest
// list and the account index already are).
func (w *writer) addKinds(kinds []string, height int64, index uint32, hash []byte) error {
	for _, kind := range kinds {
		if err := w.set(kindTxKey(kind, height, index), hash); err != nil {
			return err
		}
	}
	return nil
}

// addAccountKinds lists the transaction under each filter it matches in addr's own lists.
func (w *writer) addAccountKinds(addr string, kinds []string, height int64, index uint32, hash []byte) error {
	for _, kind := range kinds {
		if err := w.set(kindAcctKey(addr, kind, height, index), hash); err != nil {
			return err
		}
	}
	return nil
}

// listPos is where a page of a long list starts: at a numbered page (page, from 1, which makes the
// index skip every row before it) or strictly after the position a cursor names (cursor.go).
type listPos struct {
	page  int
	after *Cursor
}

func txListPrefix(filter string) []byte {
	if filter == FilterAll {
		return []byte(pfxLatest)
	}
	return kindTxPrefix(filter)
}

func accountListPrefix(addr, filter string) []byte {
	if filter == FilterAll {
		return accountPrefix(addr)
	}
	return kindAcctPrefix(addr, filter)
}

// TxsFiltered returns one page of the transactions that match filter, newest first. page starts at 1.
func (s *Store) TxsFiltered(filter string, page, limit int) ([]Tx, error) {
	return s.txPage(txListPrefix(filter), listPos{page: page}, limit, "the transaction list")
}

// TxsFilteredAfter returns one page of the transactions that match filter, newest first, that are
// older than the position of after.
func (s *Store) TxsFilteredAfter(filter string, after Cursor, limit int) ([]Tx, error) {
	return s.txPage(txListPrefix(filter), listPos{after: &after}, limit, "the transaction list")
}

// AccountTxsFiltered returns one page of the transactions whose events name addr and that match
// filter, newest first.
func (s *Store) AccountTxsFiltered(addr, filter string, page, limit int) ([]Tx, error) {
	return s.txPage(accountListPrefix(addr, filter), listPos{page: page}, limit, addr)
}

// AccountTxsFilteredAfter is AccountTxsFiltered from the position of after on.
func (s *Store) AccountTxsFilteredAfter(addr, filter string, after Cursor, limit int) ([]Tx, error) {
	return s.txPage(accountListPrefix(addr, filter), listPos{after: &after}, limit, addr)
}

// txPage reads one page of the hashes listed under prefix, newest first, and the transactions they
// name, from one snapshot so a block committed between the two cannot tear the page.
func (s *Store) txPage(prefix []byte, pos listPos, limit int, what string) ([]Tx, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	return txPageFrom(snap, prefix, pos, limit, what)
}

// txPageFrom is txPage over a snapshot the caller holds.
func txPageFrom(snap iterable, prefix []byte, pos listPos, limit int, what string) ([]Tx, error) {
	var rows []entry
	var err error
	if pos.after != nil {
		rows, err = scanBefore(snap, prefix, pos.after.bound(), limit)
	} else {
		rows, err = scan(snap, prefix, true, pos.page, limit)
	}
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
			return nil, fmt.Errorf("index of %s names transaction %x but the index does not hold it", what, row.value)
		}
		out = append(out, t)
	}
	return out, nil
}
