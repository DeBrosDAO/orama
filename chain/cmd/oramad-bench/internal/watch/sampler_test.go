package watch

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeMempool struct {
	txs int
	err error
}

func (f *fakeMempool) Unconfirmed(context.Context) (int, int64, error) {
	return f.txs, int64(f.txs) * 100, f.err
}

func TestSampler_recordsEveryNodeAndCountsFailures(t *testing.T) {
	good, bad := &fakeMempool{txs: 12}, &fakeMempool{err: errors.New("down")}
	now := t0
	s := NewSampler([]MempoolReader{good, bad, &fakeMempool{txs: 3}}, 0, func() time.Time { return now })
	s.sample(context.Background())
	now = t0.Add(time.Second)
	good.txs = 40
	s.sample(context.Background())

	all := s.Between(t0, t0.Add(time.Hour))
	if len(all) != 4 || s.Failed() != 2 {
		t.Fatalf("%d samples, %d failures", len(all), s.Failed())
	}
	if PeakTxs(all) != 40 {
		t.Fatalf("peak %d", PeakTxs(all))
	}
	late := s.Between(t0.Add(time.Second), t0.Add(time.Hour))
	if len(late) != 2 || late[0].Node != 0 || late[1].Node != 2 || late[0].Bytes != 4000 {
		t.Fatalf("late samples %+v", late)
	}
}

func TestSampler_runStopsWithTheContext(t *testing.T) {
	s := NewSampler([]MempoolReader{&fakeMempool{txs: 1}}, time.Millisecond, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	for len(s.Between(time.Time{}, time.Now().Add(time.Hour))) == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestPeakTxs_emptyIsZero(t *testing.T) {
	if PeakTxs(nil) != 0 {
		t.Fatal("peak of nothing")
	}
}

func TestFeeSeries_lastBefore(t *testing.T) {
	var s FeeSeries
	if _, ok := s.LastBefore(t0); ok {
		t.Fatal("an empty series has no last sample")
	}
	for i := 0; i < 3; i++ {
		s.Add(FeeSample{Height: int64(i), At: t0.Add(time.Duration(i) * time.Second), BaseFee: fmt.Sprint(i + 10)})
	}
	got, ok := s.LastBefore(t0.Add(1500 * time.Millisecond))
	if !ok || got.Height != 1 {
		t.Fatalf("got %+v, %v", got, ok)
	}
	if _, ok := s.LastBefore(t0); ok {
		t.Fatal("a sample at exactly t is not before t")
	}
}

func TestFeeSeries_between(t *testing.T) {
	var s FeeSeries
	for i := 0; i < 5; i++ {
		s.Add(FeeSample{Height: int64(i), At: t0.Add(time.Duration(i) * time.Second), BaseFee: "1"})
	}
	got := s.Between(t0.Add(time.Second), t0.Add(3*time.Second))
	if len(got) != 3 || got[0].Height != 1 || got[2].Height != 3 {
		t.Fatalf("got %+v", got)
	}
	if len(s.Between(t0.Add(time.Hour), t0.Add(2*time.Hour))) != 0 {
		t.Fatal("samples outside the window")
	}
}
