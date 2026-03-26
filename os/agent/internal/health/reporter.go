// Package health provides periodic health reporting to the cluster.
package health

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/orama-os/agent/internal/sandbox"
)

const (
	// ReportInterval is how often health reports are sent.
	ReportInterval = 30 * time.Second

	// GatewayHealthEndpoint is the gateway endpoint for health reports.
	GatewayHealthEndpoint = "/v1/node/health"
)

// Report represents a health report sent to the cluster.
type Report struct {
	NodeID    string          `json:"node_id"`
	Version   string          `json:"version"`
	Uptime    int64           `json:"uptime_seconds"`
	Services  map[string]bool `json:"services"`
	Healthy   bool            `json:"healthy"`
	Timestamp time.Time       `json:"timestamp"`
}

// Reporter periodically sends health reports.
type Reporter struct {
	supervisor *sandbox.Supervisor
	startTime  time.Time
	mu         sync.Mutex
	stopCh     chan struct{}
	stopped    bool
}

// NewReporter creates a new health reporter.
func NewReporter(supervisor *sandbox.Supervisor) *Reporter {
	return &Reporter{
		supervisor: supervisor,
		startTime:  time.Now(),
		stopCh:     make(chan struct{}),
	}
}

// RunLoop periodically sends health reports.
func (r *Reporter) RunLoop() {
	log.Println("health reporter started")

	ticker := time.NewTicker(ReportInterval)
	defer ticker.Stop()

	for {
		r.sendReport()

		select {
		case <-ticker.C:
		case <-r.stopCh:
			return
		}
	}
}

// Stop signals the reporter to exit.
func (r *Reporter) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stopped {
		r.stopped = true
		close(r.stopCh)
	}
}

func (r *Reporter) sendReport() {
	status := r.supervisor.GetStatus()

	healthy := true
	for _, running := range status {
		if !running {
			healthy = false
			break
		}
	}

	report := Report{
		NodeID:    readNodeID(),
		Version:   readVersion(),
		Uptime:    int64(time.Since(r.startTime).Seconds()),
		Services:  status,
		Healthy:   healthy,
		Timestamp: time.Now(),
	}

	body, err := json.Marshal(report)
	if err != nil {
		log.Printf("failed to marshal health report: %v", err)
		return
	}

	// Send to local gateway (which forwards to the cluster)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(
		"http://127.0.0.1:6001"+GatewayHealthEndpoint,
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		// Gateway may not be up yet during startup — this is expected
		return
	}
	resp.Body.Close()
}

func readNodeID() string {
	data, err := os.ReadFile("/opt/orama/.orama/configs/node-id")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

func readVersion() string {
	data, err := os.ReadFile("/etc/orama-version")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}
