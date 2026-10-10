package indexer

import (
	"encoding/json"
	"fmt"
	"time"
)

// Series intervals.
const (
	IntervalHour = "hour"
	IntervalDay  = "day"
	IntervalWeek = "week"

	secondsPerHour = 3600
	secondsPerDay  = 24 * secondsPerHour
	daysPerWeek    = 7
	// mondayShift moves the unix epoch (a Thursday) to the Monday that starts its week.
	mondayShift = 3
)

func dayOf(t time.Time) int64 { return t.UTC().Unix() / secondsPerDay }

// weekOf is the number of the week, starting on Monday, a time falls in.
func weekOf(t time.Time) int64 { return (dayOf(t) + mondayShift) / daysPerWeek }

// bucketOf is the number of the bucket of an interval that t falls in.
func bucketOf(interval string, t time.Time) int64 {
	switch interval {
	case IntervalDay:
		return dayOf(t)
	case IntervalWeek:
		return weekOf(t)
	default:
		return hourOf(t)
	}
}

func bucketKey(interval string, n int64) []byte {
	switch interval {
	case IntervalDay:
		return dayKey(n)
	case IntervalWeek:
		return weekKey(n)
	default:
		return hourKey(n)
	}
}

func bucketStart(interval string, n int64) time.Time {
	switch interval {
	case IntervalDay:
		return time.Unix(n*secondsPerDay, 0).UTC()
	case IntervalWeek:
		return time.Unix((n*daysPerWeek-mondayShift)*secondsPerDay, 0).UTC()
	default:
		return time.Unix(n*secondsPerHour, 0).UTC()
	}
}

// Series returns one page of the transaction statistics of an interval, newest first, from the
// bucket of the newest indexed block back to the bucket of the first. A bucket with no transaction
// is a bucket of zeros, so the series has no holes. It is empty on an index with no block.
func (s *Store) Series(interval string, page, limit int) ([]Bucket, error) {
	st, err := s.Status()
	if err != nil {
		return nil, err
	}
	if st.Cursor == 0 {
		return []Bucket{}, nil
	}
	first, ok, err := s.Block(st.StartHeight)
	if err != nil || !ok {
		return nil, missingBlock(st.StartHeight, err)
	}
	head, ok, err := s.Block(st.Cursor)
	if err != nil || !ok {
		return nil, missingBlock(st.Cursor, err)
	}
	lo, hi := bucketOf(interval, first.Time), bucketOf(interval, head.Time)
	out := make([]Bucket, 0, limit)
	for n := hi - int64((page-1)*limit); n >= lo && len(out) < limit; n-- {
		rec := hourRecord{Burned: "0"}
		if _, err := getJSON(s.db, bucketKey(interval, n), &rec); err != nil {
			return nil, err
		}
		out = append(out, Bucket{Start: bucketStart(interval, n), Txs: rec.Txs, Failed: rec.Failed, Burned: rec.Burned})
	}
	return out, nil
}

func missingBlock(height int64, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("index cursor is block %d but the index does not hold it", height)
}

// decodeRows decodes the JSON value of each row into a T.
func decodeRows[T any](rows []entry, what string) ([]T, error) {
	out := make([]T, 0, len(rows))
	for _, row := range rows {
		var v T
		if err := json.Unmarshal(row.value, &v); err != nil {
			return nil, fmt.Errorf("failed to decode %s: %w", what, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// Epochs returns one page of the closed epochs, newest first.
func (s *Store) Epochs(page, limit int) ([]EpochRow, error) {
	rows, err := scan(s.db, []byte(pfxEpoch), true, page, limit)
	if err != nil {
		return nil, err
	}
	return decodeRows[EpochRow](rows, "an epoch row")
}

// Epoch returns a closed epoch.
func (s *Store) Epoch(epoch uint64) (EpochRow, bool, error) {
	var row EpochRow
	ok, err := getJSON(s.db, epochKey(epoch), &row)
	return row, ok, err
}

// Supply returns one page of the supply breakdown, one point per closed epoch, newest first.
func (s *Store) Supply(page, limit int) ([]SupplyPoint, error) {
	rows, err := scan(s.db, []byte(pfxSupply), true, page, limit)
	if err != nil {
		return nil, err
	}
	return decodeRows[SupplyPoint](rows, "a supply point")
}

// EpochValidators returns one page of the validators of a closed epoch, highest voting power first.
// found is false when the epoch is not closed in the index.
func (s *Store) EpochValidators(epoch uint64, page, limit int) (rows []ValidatorEpoch, found bool, err error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	if found, err = hasKey(snap, epochKey(epoch)); err != nil || !found {
		return nil, false, err
	}
	rows, err = epochValidators(snap, epoch, page, limit)
	return rows, true, err
}

func epochValidators(db iterable, epoch uint64, page, limit int) ([]ValidatorEpoch, error) {
	prefix := powerIndexPrefix(epoch)
	idx, err := scan(db, prefix, false, page, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ValidatorEpoch, 0, len(idx))
	for _, e := range idx {
		cons := e.suffix[len(e.suffix)-consLen:]
		var row ValidatorEpoch
		ok, err := getJSON(db, valEpochKey(cons, epoch), &row)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("power index of epoch %d names validator %x that the index does not hold", epoch, cons)
		}
		out = append(out, row)
	}
	return out, nil
}

func hasKey(g getter, key []byte) (bool, error) {
	_, ok, err := getRaw(g, key)
	return ok, err
}

// ValidatorSummary is a validator as of its last closed epoch.
type ValidatorSummary struct {
	ValidatorInfo
	LastEpoch *ValidatorEpoch `json:"last_epoch,omitempty"`
}

// Validators returns one page of the validators of the newest closed epoch, highest voting power
// first. It is empty before any epoch has closed.
func (s *Store) Validators(page, limit int) ([]ValidatorSummary, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	newest, err := scan(snap, []byte(pfxEpoch), true, 1, 1)
	if err != nil || len(newest) == 0 {
		return []ValidatorSummary{}, err
	}
	var latest EpochRow
	if err := json.Unmarshal(newest[0].value, &latest); err != nil {
		return nil, fmt.Errorf("failed to decode an epoch row: %w", err)
	}
	rows, err := epochValidators(snap, latest.Epoch, page, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ValidatorSummary, 0, len(rows))
	for i := range rows {
		cons, err := decodeCons(rows[i].ConsensusAddress)
		if err != nil {
			return nil, err
		}
		var info ValidatorInfo
		if _, err := getJSON(snap, regValKey(cons), &info); err != nil {
			return nil, err
		}
		out = append(out, ValidatorSummary{ValidatorInfo: info, LastEpoch: &rows[i]})
	}
	return out, nil
}

// consensusOfOperator returns the consensus address of an operator the index has seen.
func consensusOfOperator(g getter, operator string) ([]byte, bool, error) {
	return getRaw(g, regOpKey(operator))
}

// Validator returns what the index knows of one validator.
func (s *Store) Validator(operator string) (ValidatorSummary, bool, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	cons, ok, err := consensusOfOperator(snap, operator)
	if err != nil || !ok {
		return ValidatorSummary{}, false, err
	}
	var out ValidatorSummary
	if _, err := getJSON(snap, regValKey(cons), &out.ValidatorInfo); err != nil {
		return ValidatorSummary{}, false, err
	}
	rows, err := scan(snap, valEpochPrefix(cons), true, 1, 1)
	if err != nil {
		return ValidatorSummary{}, false, err
	}
	if len(rows) == 1 {
		var last ValidatorEpoch
		if err := json.Unmarshal(rows[0].value, &last); err != nil {
			return ValidatorSummary{}, false, fmt.Errorf("failed to decode a validator epoch: %w", err)
		}
		out.LastEpoch = &last
	}
	return out, true, nil
}

// validatorRows scans one page of a validator's rows under the prefix prefixFor builds from its
// consensus address, newest first. found is false if the operator is not a validator the index has seen.
func (s *Store) validatorRows(operator string, prefixFor func([]byte) []byte, page, limit int) ([]entry, bool, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	cons, ok, err := consensusOfOperator(snap, operator)
	if err != nil || !ok {
		return nil, false, err
	}
	rows, err := scan(snap, prefixFor(cons), true, page, limit)
	return rows, true, err
}

// ValidatorEpochs returns one page of a validator's closed epochs, newest first.
func (s *Store) ValidatorEpochs(operator string, page, limit int) ([]ValidatorEpoch, bool, error) {
	rows, found, err := s.validatorRows(operator, valEpochPrefix, page, limit)
	if err != nil || !found {
		return nil, found, err
	}
	out, err := decodeRows[ValidatorEpoch](rows, "a validator epoch")
	return out, true, err
}

// ValidatorSlashes returns one page of a validator's slashes, newest first.
func (s *Store) ValidatorSlashes(operator string, page, limit int) ([]Slash, bool, error) {
	rows, found, err := s.validatorRows(operator, slashPrefix, page, limit)
	if err != nil || !found {
		return nil, found, err
	}
	out, err := decodeRows[Slash](rows, "a slash")
	return out, true, err
}

// ValidatorJails returns one page of a validator's jail periods, newest first.
func (s *Store) ValidatorJails(operator string, page, limit int) ([]JailPeriod, bool, error) {
	rows, found, err := s.validatorRows(operator, jailPrefix, page, limit)
	if err != nil || !found {
		return nil, found, err
	}
	out, err := decodeRows[JailPeriod](rows, "a jail period")
	return out, true, err
}
