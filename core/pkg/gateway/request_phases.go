package gateway

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// slowRequestThreshold is how long a request may take before loggingMiddleware
// logs where its time went, phase by phase.
const slowRequestThreshold = time.Second

// maxRequestPhases is how many steps a request marks: routing, auth, targets,
// upstream.
const maxRequestPhases = 4

type requestPhasesKey struct{}

// requestPhases records when a request finished each step the gateway takes
// for it, so a slow one says which step was slow. The soak saw authenticated
// namespace requests stall together for 3-5s while the namespace gateway
// behind the proxy answered in milliseconds, and the request log could only
// say that the whole request was slow (stagenet 2026-10-04). Every mark is
// made on the serving goroutine before the chain returns.
type requestPhases struct {
	start time.Time
	marks []phaseMark
}

type phaseMark struct {
	name string
	at   time.Time
}

func withRequestPhases(r *http.Request, start time.Time) (*http.Request, *requestPhases) {
	p := &requestPhases{start: start, marks: make([]phaseMark, 0, maxRequestPhases)}
	return r.WithContext(context.WithValue(r.Context(), requestPhasesKey{}, p)), p
}

// markPhase records that r has finished the step called name.
func markPhase(r *http.Request, name string) {
	if p, ok := r.Context().Value(requestPhasesKey{}).(*requestPhases); ok {
		p.marks = append(p.marks, phaseMark{name: name, at: time.Now()})
	}
}

// fields is each step's duration, from the end of the step before it, and
// the rest of the request after the last step, ending at end.
func (p *requestPhases) fields(end time.Time) []zap.Field {
	out := make([]zap.Field, 0, len(p.marks)+1)
	prev := p.start
	for _, m := range p.marks {
		out = append(out, zap.Int64(m.name+"_ms", m.at.Sub(prev).Milliseconds()))
		prev = m.at
	}
	return append(out, zap.Int64("rest_ms", end.Sub(prev).Milliseconds()))
}
