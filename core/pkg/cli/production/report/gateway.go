package report

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// collectGateway checks the main gateway health endpoint and parses subsystem status.
func collectGateway() *GatewayReport {
	r := &GatewayReport{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:6001/v1/health", nil)
	if err != nil {
		return r
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.Responsive = false
		return r
	}
	defer resp.Body.Close()

	r.Responsive = true
	r.HTTPStatus = resp.StatusCode

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return r
	}

	// Try to parse the health response JSON.
	// Expected: {"status":"ok","version":"...","subsystems":{"rqlite":{"status":"ok","latency":"2ms"},...}}
	var health struct {
		Status     string                       `json:"status"`
		Version    string                       `json:"version"`
		Subsystems map[string]json.RawMessage   `json:"subsystems"`
	}

	if err := json.Unmarshal(body, &health); err != nil {
		return r
	}

	r.Version = health.Version

	if len(health.Subsystems) > 0 {
		r.Subsystems = make(map[string]SubsystemHealth, len(health.Subsystems))
		for name, raw := range health.Subsystems {
			var sub SubsystemHealth
			if err := json.Unmarshal(raw, &sub); err == nil {
				r.Subsystems[name] = sub
			}
		}
	}

	return r
}
