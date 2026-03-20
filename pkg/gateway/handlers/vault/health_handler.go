package vault

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/DeBrosOfficial/network/pkg/shamir"
)

// HealthResponse is returned for GET /v1/vault/health.
type HealthResponse struct {
	Status string `json:"status"` // "healthy", "degraded", "unavailable"
}

// StatusResponse is returned for GET /v1/vault/status.
type StatusResponse struct {
	Guardians  int `json:"guardians"`   // Total guardian nodes
	Healthy    int `json:"healthy"`     // Reachable guardians
	Threshold  int `json:"threshold"`   // Read quorum (K)
	WriteQuorum int `json:"write_quorum"` // Write quorum (W)
}

// HandleHealth processes GET /v1/vault/health.
func (h *Handlers) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	guardians, err := h.discoverGuardians(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "unavailable"})
		return
	}

	n := len(guardians)
	healthy := h.probeGuardians(r.Context(), guardians)

	k := shamir.AdaptiveThreshold(n)
	wq := shamir.WriteQuorum(n)

	status := "healthy"
	if healthy < wq {
		if healthy >= k {
			status = "degraded"
		} else {
			status = "unavailable"
		}
	}

	writeJSON(w, http.StatusOK, HealthResponse{Status: status})
}

// HandleStatus processes GET /v1/vault/status.
func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	guardians, err := h.discoverGuardians(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, StatusResponse{})
		return
	}

	n := len(guardians)
	healthy := h.probeGuardians(r.Context(), guardians)

	writeJSON(w, http.StatusOK, StatusResponse{
		Guardians:   n,
		Healthy:     healthy,
		Threshold:   shamir.AdaptiveThreshold(n),
		WriteQuorum: shamir.WriteQuorum(n),
	})
}

// probeGuardians checks health of all guardians in parallel and returns the healthy count.
func (h *Handlers) probeGuardians(ctx context.Context, guardians []guardian) int {
	ctx, cancel := context.WithTimeout(ctx, guardianTimeout)
	defer cancel()

	var healthyCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(len(guardians))

	for _, g := range guardians {
		go func(gd guardian) {
			defer wg.Done()

			url := fmt.Sprintf("http://%s:%d/v1/vault/health", gd.IP, gd.Port)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return
			}

			resp, err := h.httpClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				healthyCount.Add(1)
			}
		}(g)
	}

	wg.Wait()
	return int(healthyCount.Load())
}
