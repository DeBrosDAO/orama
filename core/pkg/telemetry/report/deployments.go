package report

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// deployUnitPattern matches every deployment process unit
// (orama-deploy-<runtime>@<namespace>-<name>.service).
const deployUnitPattern = "orama-deploy-*"

// collectDeployments counts the deployment processes this node runs: its
// orama-deploy-* units. systemd is the one source — the gateway's /v1/health
// reports no deployment detail, and the registry's deployment rows describe
// the cluster, not what runs here. --all keeps a stopped deployment in the
// total; --plain drops the "●" marker systemd prints before a failed unit,
// which would shift the columns.
func collectDeployments() *DeploymentsReport {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runCmd(ctx, "systemctl", "list-units", "--all", "--plain", "--type=service",
		"--no-legend", "--no-pager", deployUnitPattern)
	if err != nil {
		return &DeploymentsReport{Error: fmt.Sprintf("list %s units: %v", deployUnitPattern, err)}
	}
	return parseDeploymentUnits(out)
}

// parseDeploymentUnits counts `systemctl list-units --plain --no-legend`
// lines: UNIT LOAD ACTIVE SUB DESCRIPTION. A unit whose file is gone
// (LOAD not-found) is not a deployment any more and is not counted.
func parseDeploymentUnits(out string) *DeploymentsReport {
	report := &DeploymentsReport{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "loaded" {
			continue
		}
		report.TotalCount++
		if fields[3] == "running" {
			report.RunningCount++
		}
		if fields[2] == "failed" {
			report.FailedCount++
		}
	}
	return report
}

// collectServerless reports the serverless engine's status. The WASM engine
// runs inside the index gateway, so its status is the gateway's /v1/health
// answer on constants.GatewayAPIPort.
func collectServerless() *ServerlessReport {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return serverlessStatus(ctx, constants.LocalGatewayURL()+"/v1/health")
}

// serverlessStatus probes healthURL: "healthy" on 200, "unhealthy (HTTP n)"
// otherwise, "unreachable" when nothing answers.
func serverlessStatus(ctx context.Context, healthURL string) *ServerlessReport {
	report := &ServerlessReport{EngineStatus: "unknown"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		report.EngineStatus = fmt.Sprintf("unknown (bad health URL: %v)", err)
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
