package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
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
		if out, err := runCmd(ctx, "systemctl", "is-active", "orama-namespace-ipfs@index"); err == nil && strings.TrimSpace(out) == "active" {
			r.DaemonActive = true
		} else if out, err := runCmd(ctx, "systemctl", "is-active", "orama-ipfs"); err == nil {
			r.DaemonActive = strings.TrimSpace(out) == "active"
		}
	}

	// 2. ClusterActive: systemctl is-active orama-ipfs-cluster
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "systemctl", "is-active", "orama-namespace-ipfs-cluster@index"); err == nil && strings.TrimSpace(out) == "active" {
			r.ClusterActive = true
		} else if out, err := runCmd(ctx, "systemctl", "is-active", "orama-ipfs-cluster"); err == nil {
			r.ClusterActive = strings.TrimSpace(out) == "active"
		}
	}

	// 3. SwarmPeerCount: POST /api/v0/swarm/peers
	{
		body, err := ipfsPost(constants.LocalIPFSAPIURL() + "/api/v0/swarm/peers")
		if err == nil {
			var resp struct {
				Peers []interface{} `json:"Peers"`
			}
			if err := json.Unmarshal(body, &resp); err == nil {
				r.SwarmPeerCount = len(resp.Peers)
			}
		}
	}

	// 4. ClusterPeerCount: GET /peers (with fallback to /id)
	//    The /peers endpoint does a synchronous round-trip to ALL cluster peers,
	//    so it can be slow if some peers are unreachable (ghost WG entries, etc.).
	//    Use a generous timeout and fall back to /id if /peers times out.
	//    The REST API requires the password derived from the cluster secret; it
	//    used to be asked on 9094, where nothing listens, so these were empty.
	clusterPassword, clusterErr := ipfs.LocalClusterRESTPassword()
	if clusterErr != nil {
		r.ClusterError = clusterErr.Error()
	}
	if clusterErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if body, err := clusterGet(ctx, "/peers", clusterPassword); err == nil {
			var peers []interface{}
			if err := json.Unmarshal(body, &peers); err == nil {
				r.ClusterPeerCount = len(peers)
			}
		}
	}
	// Fallback: if /peers returned 0 (timeout or error), try /id which returns
	// cached cluster_peers instantly without contacting other nodes.
	if clusterErr == nil && r.ClusterPeerCount == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if body, err := clusterGet(ctx, "/id", clusterPassword); err == nil {
			var resp struct {
				ClusterPeers []string `json:"cluster_peers"`
			}
			if err := json.Unmarshal(body, &resp); err == nil && len(resp.ClusterPeers) > 0 {
				// cluster_peers includes self, so count is len(cluster_peers)
				r.ClusterPeerCount = len(resp.ClusterPeers)
			}
		}
	}

	// 5. RepoSizeBytes/RepoMaxBytes: POST /api/v0/repo/stat
	{
		body, err := ipfsPost(constants.LocalIPFSAPIURL() + "/api/v0/repo/stat")
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
		body, err := ipfsPost(constants.LocalIPFSAPIURL() + "/api/v0/version")
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
	if clusterErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if body, err := clusterGet(ctx, "/id", clusterPassword); err == nil {
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
		body, err := ipfsPost(constants.LocalIPFSAPIURL() + "/api/v0/bootstrap/list")
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

	token, err := ipfs.LocalKuboAPIToken()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// clusterGet GETs path from this node's IPFS Cluster REST API with its
// basic-auth password.
func clusterGet(ctx context.Context, path, password string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, constants.LocalIPFSClusterURL()+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(ipfs.ClusterRESTUser, password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IPFS Cluster %s: HTTP %d", path, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
