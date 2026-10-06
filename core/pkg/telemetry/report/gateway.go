package report

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// collectGateway checks the index gateway's health endpoint and parses each
// subsystem check, then reads its build version.
func collectGateway() *GatewayReport {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return gatewayStatus(ctx, constants.LocalGatewayURL())
}

// gatewayStatus reads /v1/health and /v1/version from the gateway at base.
//
// The open /v1/health answers {"status","server","checks":{name:{"status"}}}
// (pkg/gateway publicHealth); each check's error is only in the operator
// report. It carries no version: that is /v1/version's "version".
func gatewayStatus(ctx context.Context, base string) *GatewayReport {
	r := &GatewayReport{}
	healthURL := base + "/v1/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return r
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return r
	}
	defer resp.Body.Close()

	r.Responsive = true
	r.HTTPStatus = resp.StatusCode

	body, err := readLocalBody(resp.Body, healthURL)
	if err != nil {
		return r
	}
	var health struct {
		Checks map[string]SubsystemHealth `json:"checks"`
	}
	if err := json.Unmarshal(body, &health); err == nil && len(health.Checks) > 0 {
		r.Subsystems = health.Checks
	}

	if body, err := httpGet(ctx, base+"/v1/version"); err == nil {
		var v struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(body, &v) == nil {
			r.Version = v.Version
		}
	}
	return r
}
