package report

import (
	"context"
	"encoding/json"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"strconv"
	"strings"
	"time"
)

// Each budget is its own: the status probe used to share one 5 s budget with
// four shell commands run before it, and on a loaded node those spent it, so a
// vault that answered was reported unresponsive and the cluster read
// "degraded".
const (
	vaultDetailsTimeout = 5 * time.Second
	vaultStatusTimeout  = 5 * time.Second
)

func collectVault() *VaultReport {
	r := &VaultReport{}
	statusCtx, cancelStatus := context.WithTimeout(context.Background(), vaultStatusTimeout)
	probeVaultStatus(statusCtx, constants.LocalGatewayURL()+"/v1/vault/status", r)
	probeVaultHealth(statusCtx, constants.LocalGatewayURL()+"/v1/vault/health", r)
	cancelStatus()

	ctx, cancel := context.WithTimeout(context.Background(), vaultDetailsTimeout)
	defer cancel()

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

	return r
}

// probeVaultStatus reads the guardian health the gateway reports for the vault
// into r; a vault that answers is Responsive.
func probeVaultStatus(ctx context.Context, url string, r *VaultReport) {
	body, err := httpGet(ctx, url)
	if err != nil {
		return
	}
	var status struct {
		Guardians   int `json:"guardians"`
		Healthy     int `json:"healthy"`
		Threshold   int `json:"threshold"`
		WriteQuorum int `json:"write_quorum"`
	}
	if json.Unmarshal(body, &status) != nil {
		return
	}
	r.Responsive = true
	r.Guardians = status.Guardians
	r.Healthy = status.Healthy
	r.Threshold = status.Threshold
	r.WriteQuorum = status.WriteQuorum
}

// probeVaultHealth reads the vault's overall health ("healthy", "degraded",
// "unavailable") into r.Status, which the vault alerts are raised from.
func probeVaultHealth(ctx context.Context, url string, r *VaultReport) {
	body, err := httpGet(ctx, url)
	if err != nil {
		return
	}
	var health struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &health) == nil {
		r.Status = health.Status
	}
}
