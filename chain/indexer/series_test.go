package indexer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seriesChain has a block every 5 hours, 40 of them (more than a week), each with one send.
func seriesChain(t *testing.T) (*Store, *fakeChain) {
	t.Helper()
	alice, bob := addr(t, 1), addr(t, 2)
	chain := newFakeChain()
	chain.step = 5 * 3600
	for h := 0; h < 40; h++ {
		if h%7 == 3 {
			chain.add(failedTx(t, nil, alice))
		} else {
			chain.add(bankSend(t, alice, bob))
		}
	}
	return index(t, chain, 1), chain
}

// startOf is the start of the hour, day or week (Monday) that t falls in, worked out without the
// index's arithmetic.
func startOf(interval string, t time.Time) time.Time {
	switch interval {
	case IntervalDay:
		return t.Truncate(24 * time.Hour)
	case IntervalWeek:
		day := t.Truncate(24 * time.Hour)
		return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	default:
		return t.Truncate(time.Hour)
	}
}

func step(interval string, t time.Time, n int) time.Time {
	switch interval {
	case IntervalDay:
		return t.AddDate(0, 0, n)
	case IntervalWeek:
		return t.AddDate(0, 0, 7*n)
	default:
		return t.Add(time.Duration(n) * time.Hour)
	}
}

func TestSeries_everyIntervalCountsTheTransactionsOfItsBuckets(t *testing.T) {
	store, chain := seriesChain(t)
	for _, interval := range []string{IntervalHour, IntervalDay, IntervalWeek} {
		type counts struct{ txs, failed uint64 }
		want := map[time.Time]counts{}
		for h := int64(1); h <= 40; h++ {
			c := want[startOf(interval, chain.timeOf(h))]
			c.txs++
			if (h-1)%7 == 3 {
				c.failed++
			}
			want[startOf(interval, chain.timeOf(h))] = c
		}
		last, first := startOf(interval, chain.timeOf(40)), startOf(interval, chain.timeOf(1))

		got, err := store.Series(interval, 1, MaxLimit)
		require.NoError(t, err)
		var i int
		for at := last; !at.Before(first); at = step(interval, at, -1) {
			if i >= MaxLimit {
				break
			}
			require.Equal(t, at, got[i].Start, "%s %d", interval, i)
			require.Equal(t, want[at].txs, got[i].Txs, "%s %s", interval, at)
			require.Equal(t, want[at].failed, got[i].Failed, "%s %s", interval, at)
			i++
		}
		if interval != IntervalHour {
			require.Len(t, got, i, "%s: from the newest bucket back to the first, no more", interval)
		}
	}
}

func TestSeries_hourlyHasNoHolesAndPagesContinueWhereTheLastEnded(t *testing.T) {
	store, _ := seriesChain(t)
	page1, err := store.Series(IntervalHour, 1, 10)
	require.NoError(t, err)
	page2, err := store.Series(IntervalHour, 2, 10)
	require.NoError(t, err)
	require.Len(t, page1, 10)
	require.Len(t, page2, 10)
	require.Equal(t, page1[9].Start.Add(-time.Hour), page2[0].Start)
	var zeros int
	for _, b := range page1 {
		if b.Txs == 0 {
			zeros++
			require.Equal(t, "0", b.Burned)
		}
	}
	require.Equal(t, 8, zeros, "a block every 5 hours leaves 4 of 5 hours empty, and they are in the series as zeros")

	far, err := store.Series(IntervalHour, MaxPage, MaxLimit)
	require.NoError(t, err)
	require.Empty(t, far, "a page past the oldest bucket is empty")
}

func TestSeries_isEmptyBeforeTheFirstBlock(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	got, err := store.Series(IntervalDay, 1, 10)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestSeries_theFortyEightHourWindowIsUnchanged(t *testing.T) {
	store, _ := seriesChain(t)
	hours, err := store.Stats()
	require.NoError(t, err)
	require.Len(t, hours, HourlyWindow)
	series, err := store.Series(IntervalHour, 1, HourlyWindow)
	require.NoError(t, err)
	for i := range hours {
		require.Equal(t, hours[HourlyWindow-1-i].Hour, series[i].Start)
		require.Equal(t, hours[HourlyWindow-1-i].Txs, series[i].Txs)
	}
}
