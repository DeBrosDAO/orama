package indexer

import (
	"encoding/json"
	"fmt"
	"math"

	cosmath "cosmossdk.io/math"
)

// consLen is the length of a consensus address: the first 20 bytes of the SHA-256 of the key.
const consLen = 20

func epochKey(epoch uint64) []byte  { return join([]byte(pfxEpoch), be64(epoch)) }
func supplyKey(epoch uint64) []byte { return join([]byte(pfxSupply), be64(epoch)) }
func valEpochPrefix(cons []byte) []byte {
	return join([]byte(pfxValEp), cons)
}
func valEpochKey(cons []byte, epoch uint64) []byte { return join(valEpochPrefix(cons), be64(epoch)) }
func powerIndexPrefix(epoch uint64) []byte         { return join([]byte(pfxPowerIx), be64(epoch)) }

// powerIndexKey sorts the validators of an epoch by power, highest first, then by consensus address.
func powerIndexKey(epoch uint64, power int64, cons []byte) []byte {
	if power < 0 {
		power = 0
	}
	return join(powerIndexPrefix(epoch), be64(math.MaxUint64-uint64(power)), cons)
}
func counterPrefix(epoch uint64) []byte { return join([]byte(pfxCounter), be64(epoch)) }
func counterKey(epoch uint64, cons []byte) []byte {
	return join(counterPrefix(epoch), cons)
}
func slashPrefix(cons []byte) []byte { return join([]byte(pfxSlash), cons) }
func slashKey(cons []byte, height int64, idx uint32) []byte {
	return join(slashPrefix(cons), be64(uint64(height)), be32(idx))
}
func jailPrefix(cons []byte) []byte            { return join([]byte(pfxJail), cons) }
func jailKey(cons []byte, height int64) []byte { return join(jailPrefix(cons), be64(uint64(height))) }
func regValKey(cons []byte) []byte             { return join([]byte(pfxRegVal), cons) }
func regOpKey(operator string) []byte          { return []byte(pfxRegOp + operator) }
func dayKey(day int64) []byte                  { return join([]byte(pfxDay), be64(uint64(day))) }
func weekKey(week int64) []byte                { return join([]byte(pfxWeek), be64(uint64(week))) }

func (w *writer) watch() (watch, bool, error) {
	var wt watch
	ok, err := getJSON(w.b, []byte(keyWatch), &wt)
	return wt, ok, err
}

func (w *writer) putWatch(wt watch) error { return w.setJSON([]byte(keyWatch), wt) }

// counters returns a validator's tallies in the epoch in progress.
func (w *writer) counters(epoch uint64, cons []byte) (counters, error) {
	c := counters{Burned: cosmath.ZeroInt()}
	_, err := getJSON(w.b, counterKey(epoch, cons), &c)
	return c, err
}

func (w *writer) putCounters(epoch uint64, cons []byte, c counters) error {
	return w.setJSON(counterKey(epoch, cons), c)
}

// epochCounters returns every validator's tallies for the epoch, keyed by consensus address.
func (w *writer) epochCounters(epoch uint64) (map[string]counters, error) {
	prefix := counterPrefix(epoch)
	rows, err := scan(w.b, prefix, false, 1, math.MaxInt32)
	if err != nil {
		return nil, err
	}
	out := make(map[string]counters, len(rows))
	for _, row := range rows {
		c := counters{Burned: cosmath.ZeroInt()}
		if err := json.Unmarshal(row.value, &c); err != nil {
			return nil, fmt.Errorf("failed to decode the tallies of epoch %d: %w", epoch, err)
		}
		out[string(row.suffix)] = c
	}
	return out, nil
}

// dropEpochCounters deletes the tallies of an epoch that has closed.
func (w *writer) dropEpochCounters(epoch uint64) error {
	prefix := counterPrefix(epoch)
	if err := w.b.DeleteRange(prefix, prefixEnd(prefix), nil); err != nil {
		return fmt.Errorf("failed to stage delete of the tallies of epoch %d: %w", epoch, err)
	}
	return nil
}

func (w *writer) validator(cons []byte) (ValidatorInfo, bool, error) {
	var v ValidatorInfo
	ok, err := getJSON(w.b, regValKey(cons), &v)
	return v, ok, err
}

func (w *writer) putValidator(cons []byte, v ValidatorInfo) error {
	if err := w.setJSON(regValKey(cons), v); err != nil {
		return err
	}
	return w.set(regOpKey(v.Operator), cons)
}

// consensusOf returns the consensus address of an operator the index has seen.
func (w *writer) consensusOf(operator string) ([]byte, bool, error) {
	return consensusOfOperator(w.b, operator)
}

// latestJail returns the newest jail period of a validator.
func (w *writer) latestJail(cons []byte) ([]byte, JailPeriod, bool, error) {
	rows, err := scan(w.b, jailPrefix(cons), true, 1, 1)
	if err != nil || len(rows) == 0 {
		return nil, JailPeriod{}, false, err
	}
	var p JailPeriod
	if err := json.Unmarshal(rows[0].value, &p); err != nil {
		return nil, JailPeriod{}, false, fmt.Errorf("failed to decode a jail period: %w", err)
	}
	return join(jailPrefix(cons), rows[0].suffix), p, true, nil
}
