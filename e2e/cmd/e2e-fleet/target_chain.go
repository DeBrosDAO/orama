package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	// chainStatusPath is the gateway's proxy of the chain's CometBFT /status.
	chainStatusPath = "/v1/chain/status"
	// chainStatusTimeout bounds the one read of the live chain id.
	chainStatusTimeout = 20 * time.Second
	// chainStatusLimit caps the /status body read.
	chainStatusLimit = 1 << 20
)

// chainIDReader returns the chain id the target's gateway reports; tests replace it.
type chainIDReader func(ctx context.Context) (string, error)

// resolveChainID is the chain id a stagenet run is written for: the one the
// live chain reports. A reset gives the chain a new id, so a remembered default
// goes stale with the first reset; --chain-id, when given, must name the chain
// that is running.
func resolveChainID(ctx context.Context, flagID string, live chainIDReader) (string, error) {
	id, err := live(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to read the chain id from %s%s: %w", config.StagenetGatewayURL, chainStatusPath, err)
	}
	if err := config.CheckStagenetChainID(id); err != nil {
		return "", fmt.Errorf("the live chain reports an id this target refuses: %w", err)
	}
	if flagID != "" && flagID != id {
		return "", fmt.Errorf("--chain-id %s does not match the running chain %s", flagID, id)
	}
	return id, nil
}

// gatewayChainID reads node_info.network from the gateway's /v1/chain/status,
// trusting only the run's pinned roots.
func gatewayChainID(caFile string) chainIDReader {
	return func(ctx context.Context) (string, error) {
		pool, err := gw.LoadCAPool(caFile)
		if err != nil {
			return "", err
		}
		client := &http.Client{Timeout: chainStatusTimeout, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.StagenetGatewayURL+chainStatusPath, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, chainStatusLimit))
		if err != nil {
			return "", fmt.Errorf("read the status: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return parseChainStatusNetwork(body)
	}
}

// parseChainStatusNetwork is result.node_info.network of a CometBFT /status answer.
func parseChainStatusNetwork(body []byte) (string, error) {
	var st struct {
		Result struct {
			NodeInfo struct {
				Network string `json:"network"`
			} `json:"node_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return "", fmt.Errorf("decode the status: %w", err)
	}
	if st.Result.NodeInfo.Network == "" {
		return "", fmt.Errorf("the status names no network")
	}
	return st.Result.NodeInfo.Network, nil
}
