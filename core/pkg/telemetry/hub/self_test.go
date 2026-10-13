package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func TestSelfCollectorLatest_beforeFirstCollection(t *testing.T) {
	s := &SelfCollector{}
	if r, err := s.Latest(); r != nil || !errors.Is(err, ErrNoReportYet) {
		t.Fatalf("r=%v err=%v, want ErrNoReportYet", r, err)
	}
}

func TestSelfCollectorCollectOnce_decoratesReport(t *testing.T) {
	s := &SelfCollector{
		Collect:  func(context.Context) ([]byte, error) { return []byte(`{"hostname":"n1"}`), nil },
		Decorate: func(r *report.NodeReport) { r.Traffic = &report.TrafficReport{RPS: 3} },
		Timeout:  time.Second,
	}
	s.collectOnce(context.Background())
	r, err := s.Latest()
	if err != nil || r.Hostname != "n1" || r.Traffic == nil || r.Traffic.RPS != 3 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
}

func TestSelfCollectorCollectOnce_failureKeepsPreviousReport(t *testing.T) {
	calls := 0
	s := &SelfCollector{
		Collect: func(context.Context) ([]byte, error) {
			calls++
			if calls == 1 {
				return []byte(`{"hostname":"n1"}`), nil
			}
			return nil, errors.New("privhelper socket missing")
		},
		Timeout: time.Second,
	}
	s.collectOnce(context.Background())
	s.collectOnce(context.Background())
	r, err := s.Latest()
	if r == nil || r.Hostname != "n1" {
		t.Fatalf("previous report lost: %+v", r)
	}
	if err == nil {
		t.Fatal("the failed collection's error was not reported")
	}
}

func TestSelfCollectorCollectOnce_badJSON(t *testing.T) {
	s := &SelfCollector{Collect: func(context.Context) ([]byte, error) { return []byte("{"), nil }, Timeout: time.Second}
	s.collectOnce(context.Background())
	if r, err := s.Latest(); r != nil || err == nil {
		t.Fatalf("r=%v err=%v, want no report and a parse error", r, err)
	}
}

func TestSelfCollectorRun_stopsWithContext(t *testing.T) {
	s := &SelfCollector{
		Collect:  func(context.Context) ([]byte, error) { return []byte(`{}`), nil },
		Interval: time.Hour,
		Timeout:  time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
