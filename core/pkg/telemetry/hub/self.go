package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// ErrNoReportYet is returned before the first collection has finished.
var ErrNoReportYet = errors.New("this node has not finished its first health collection yet")

// SelfCollector keeps this node's own report fresh on a timer.
type SelfCollector struct {
	// Collect returns the node report as JSON. On a node it asks the root
	// helper, since most of the report needs root (privhelper.NodeReport).
	Collect func(ctx context.Context) ([]byte, error)
	// Decorate adds what the collector cannot see, such as the traffic this
	// gateway served. It may be nil.
	Decorate func(r *report.NodeReport)
	Interval time.Duration
	// Timeout bounds one collection.
	Timeout time.Duration
	Logger  *zap.Logger

	mu     sync.RWMutex
	latest *report.NodeReport
	err    error
}

// Run collects immediately, then every Interval until ctx is done.
func (s *SelfCollector) Run(ctx context.Context) {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		s.collectOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *SelfCollector) collectOnce(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	r, err := s.collect(cctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		// Keep the previous report: its timestamp tells readers how old it
		// is, and the error says why no newer one exists.
		s.err = err
		if s.Logger != nil {
			s.Logger.Warn("node health collection failed", zap.Error(err))
		}
		return
	}
	s.latest, s.err = r, nil
}

func (s *SelfCollector) collect(ctx context.Context) (*report.NodeReport, error) {
	raw, err := s.Collect(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect this node's health report: %w", err)
	}
	var r report.NodeReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse this node's health report: %w", err)
	}
	if s.Decorate != nil {
		s.Decorate(&r)
	}
	return &r, nil
}

// Latest returns the most recent report. The error is set when the last
// collection failed; the report is still the newest one there is, possibly
// nil if none ever succeeded.
func (s *SelfCollector) Latest() (*report.NodeReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.latest == nil && s.err == nil {
		return nil, ErrNoReportYet
	}
	return s.latest, s.err
}
