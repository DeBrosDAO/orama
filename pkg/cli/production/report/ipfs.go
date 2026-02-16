package report

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// collectIPFS gathers IPFS daemon and cluster health information.
func collectIPFS() *IPFSReport {
	r := &IPFSReport{}

	// 1. DaemonActive: systemctl is-active orama-ipfs
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "orama-ipfs"); err == nil {
			r.DaemonActive = strings.TrimSpace(out) == "active"
		}
	}

	// 2. ClusterActive: systemctl is-active orama-ipfs-cluster
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "orama-ipfs-cluster"); err == nil {
			r.ClusterActive = strings.TrimSpace(out) == "active"
		}
	}

	// 3. SwarmPeerCount: POST /api/v0/swarm/peers
	{
		body, err := ipfsPost("http://localhost:4501/api/v0/swarm/peers")
		if err == nil {
			var resp struct {
				Peers []interface{} `json:"Peers"`
			}
			if err := json.Unmarshal(body, &resp); err == nil {
				r.SwarmPeerCount = len(resp.Peers)
			}
		}
	}

	// 4. ClusterPeerCount: GET /peers
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if body, err := httpGet(ctx, "http://localhost:9094/peers"); err == nil {
			var peers []interface{}
			if err := json.Unmarshal(body, &peers); err == nil {
				r.ClusterPeerCount = len(peers)
			}
		}
	}

	// 5. RepoSizeBytes/RepoMaxBytes: POST /api/v0/repo/stat
	{
		body, err := ipfsPost("http://localhost:4501/api/v0/repo/stat")
		if err == nil {
			var resp struct {
				RepoSize   int64 `json:"RepoSize"`
				StorageMax int64 `json:"StorageMax"`
			}
			if err := json.Unmarshal(body, &resp); err == nil {
				r.RepoSizeBytes = resp.RepoSize
				r.RepoMaxBytes = resp.StorageMax
				if resp.StorageMax > 0 && resp.RepoSize > 0 {
					r.RepoUsePct = int(float64(resp.RepoSize) / float64(resp.StorageMax) * 100)
				}
			}
		}
	}

	// 6. KuboVersion: POST /api/v0/version
	{
		body, err := ipfsPost("http://localhost:4501/api/v0/version")
		if err == nil {
			var resp struct {
				Version string `json:"Version"`
			}
			if err := json.Unmarshal(body, &resp); err == nil {
				r.KuboVersion = resp.Version
			}
		}
	}

	// 7. ClusterVersion: GET /id
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if body, err := httpGet(ctx, "http://localhost:9094/id"); err == nil {
			var resp struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(body, &resp); err == nil {
				r.ClusterVersion = resp.Version
			}
		}
	}

	// 8. HasSwarmKey: check file existence
	if _, err := os.Stat("/opt/orama/.orama/data/ipfs/repo/swarm.key"); err == nil {
		r.HasSwarmKey = true
	}

	// 9. BootstrapEmpty: POST /api/v0/bootstrap/list
	{
		body, err := ipfsPost("http://localhost:4501/api/v0/bootstrap/list")
		if err == nil {
			var resp struct {
				Peers []interface{} `json:"Peers"`
			}
			if err := json.Unmarshal(body, &resp); err == nil {
				r.BootstrapEmpty = resp.Peers == nil || len(resp.Peers) == 0
			} else {
				// If we got a response but Peers is missing, treat as empty.
				r.BootstrapEmpty = true
			}
		}
	}

	return r
}

// ipfsPost sends a POST request with an empty body to an IPFS API endpoint.
// IPFS uses POST for all API calls. Uses a 3-second timeout.
func ipfsPost(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
