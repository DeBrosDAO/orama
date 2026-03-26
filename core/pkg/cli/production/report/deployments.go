package report

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// collectDeployments discovers deployed applications by querying the local gateway.
func collectDeployments() *DeploymentsReport {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report := &DeploymentsReport{}

	// Query the local gateway for deployment list
	url := "http://localhost:8080/v1/health"
	body, err := httpGet(ctx, url)
	if err != nil {
		// Gateway not available — fall back to systemd unit discovery
		return collectDeploymentsFromSystemd()
	}

	// Check if gateway reports deployment counts in health response
	var health map[string]interface{}
	if err := json.Unmarshal(body, &health); err == nil {
		if deps, ok := health["deployments"].(map[string]interface{}); ok {
			if v, ok := deps["total"].(float64); ok {
				report.TotalCount = int(v)
			}
			if v, ok := deps["running"].(float64); ok {
				report.RunningCount = int(v)
			}
			if v, ok := deps["failed"].(float64); ok {
				report.FailedCount = int(v)
			}
			return report
		}
	}

	// Fallback: count deployment systemd units
	return collectDeploymentsFromSystemd()
}

// collectDeploymentsFromSystemd discovers deployments by listing systemd units.
func collectDeploymentsFromSystemd() *DeploymentsReport {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report := &DeploymentsReport{}

	// List orama-deploy-* units
	out, err := runCmd(ctx, "systemctl", "list-units", "--type=service", "--no-legend", "--no-pager", "orama-deploy-*")
	if err != nil {
		return report
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		report.TotalCount++
		fields := strings.Fields(line)
		// systemctl list-units format: UNIT LOAD ACTIVE SUB DESCRIPTION...
		if len(fields) >= 4 {
			switch fields[3] {
			case "running":
				report.RunningCount++
			case "failed", "dead":
				report.FailedCount++
			}
		}
	}

	return report
}

// collectServerless checks if the serverless engine is available via the gateway health endpoint.
func collectServerless() *ServerlessReport {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report := &ServerlessReport{
		EngineStatus: "unknown",
	}

	// Check gateway health for serverless subsystem
	url := "http://localhost:8080/v1/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return report
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		report.EngineStatus = "unreachable"
		return report
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		report.EngineStatus = "healthy"
	} else {
		report.EngineStatus = fmt.Sprintf("unhealthy (HTTP %d)", resp.StatusCode)
	}

	return report
}
