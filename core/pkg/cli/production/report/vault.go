package report

import (
	"context"
	"encoding/json"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"strconv"
	"strings"
	"time"
)

func collectVault() *VaultReport {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := &VaultReport{}

	// 1. Service active
	if out, err := runCmd(ctx, "systemctl", "is-active", "orama-namespace-vault@index"); err == nil && strings.TrimSpace(out) == "active" {
		r.ServiceActive = true
	} else if out, err := runCmd(ctx, "systemctl", "is-active", "orama-vault"); err == nil {
		r.ServiceActive = strings.TrimSpace(out) == "active"
	}

	// 2. Restart count
	if out, err := runCmd(ctx, "systemctl", "show", "orama-namespace-vault@index", "--property=NRestarts"); err == nil {
		if parts := strings.SplitN(out, "=", 2); len(parts) == 2 {
			r.RestartCount, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
		}
	}

	// 3. Process memory
	if out, err := runCmd(ctx, "systemctl", "show", "orama-namespace-vault@index", "--property=MemoryCurrent"); err == nil {
		if parts := strings.SplitN(out, "=", 2); len(parts) == 2 {
			r.ProcessMemMB = parseMemoryMB(parts[1])
		}
	}

	// 4. Log errors in last hour
	if out, err := runCmd(ctx, "bash", "-c",
		`journalctl -u orama-namespace-vault@index -u orama-vault --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE "(error|ERR)" || echo 0`); err == nil {
		r.LogErrors, _ = strconv.Atoi(strings.TrimSpace(out))
	}

	// 5. Query vault status via gateway (provides guardian health)
	if body, err := httpGet(ctx, constants.LocalGatewayURL()+"/v1/vault/status"); err == nil {
		var status struct {
			Guardians   int `json:"guardians"`
			Healthy     int `json:"healthy"`
			Threshold   int `json:"threshold"`
			WriteQuorum int `json:"write_quorum"`
		}
		if json.Unmarshal(body, &status) == nil {
			r.Responsive = true
			r.Guardians = status.Guardians
			r.Healthy = status.Healthy
			r.Threshold = status.Threshold
			r.WriteQuorum = status.WriteQuorum
		}
	}

	// 6. Query vault health status
	if body, err := httpGet(ctx, constants.LocalGatewayURL()+"/v1/vault/health"); err == nil {
		var health struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(body, &health) == nil {
			r.Status = health.Status
		}
	}

	return r
}
