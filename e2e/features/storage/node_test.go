//go:build e2e_fleet

package storage

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	gcTimer   = "orama-namespace-ipfs-gc@index.timer"
	gcEnv     = "/var/lib/orama-unit-env/index/ipfs-gc.env"
	svcJSON   = "/opt/orama/.orama/data/ipfs-cluster/service.json"
	ownerUser = "orama"
	// storageMaxFraction and storageMaxFloorGB: Datastore.StorageMax is half
	// the repo filesystem, floored at 10GB (core/pkg/install/installers/ipfs.go).
	storageMaxFraction = 0.5
	storageMaxFloorGB  = 10
)

// TestReplication_readableThroughEveryNode: once pinned on RF peers, the
// object downloads through every node's gateway (docs/ARCHITECTURE.md).
func TestReplication_readableThroughEveryNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	data := randomBytes(t, smallBytes)
	u := upload(t, n.Client, tenancy.Owner(n), "everywhere.bin", data)
	waitPinned(t, n.Client, tenancy.Owner(n), u.Cid)
	for _, node := range f.State.Nodes {
		t.Run(node.Name, func(t *testing.T) {
			waitContent(t, n.Client.PinTo(node.PublicIP), tenancy.Owner(n), u.Cid, data)
		})
	}
}

// TestSealing_privateBlobIsCiphertextOnNode: a storage upload is AES-GCM
// sealed before Add, so every node's Kubo repo holds ORMAW1 ciphertext and
// never the plaintext; a .tar.gz is not wrapped (docs/SECURITY.md, feat-270).
func TestSealing_privateBlobIsCiphertextOnNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	marker := "E2E-PLAINTEXT-" + f.State.RunID + "-" + strconv.Itoa(int(randomBytes(t, 1)[0]))
	private := upload(t, n.Client, tenancy.Owner(n), "diary.txt", []byte(marker))
	tarball := upload(t, n.Client, tenancy.Owner(n), "site.tar.gz", []byte(marker))
	waitPinned(t, n.Client, tenancy.Owner(n), private.Cid)
	waitPinned(t, n.Client, tenancy.Owner(n), tarball.Cid)
	for _, node := range f.State.Nodes {
		p := services.Kubo(t, f, node, "/api/v0/cat?arg="+private.Cid, true)
		if p.Status != http.StatusOK || !strings.HasPrefix(p.Body, wrapMagic) || strings.Contains(p.Body, marker) {
			t.Errorf("%s: the stored private blob is not ORMAW1 ciphertext (status %d, starts %.8q)", node.Name, p.Status, p.Body)
		}
		p = services.Kubo(t, f, node, "/api/v0/cat?arg="+tarball.Cid, true)
		if p.Status != http.StatusOK || p.Body != marker {
			t.Errorf("%s: a .tar.gz should be stored as is (status %d)", node.Name, p.Status)
		}
	}
	waitContent(t, n.Client, tenancy.Owner(n), private.Cid, []byte(marker))
}

// TestKubo_bearerRequired: the Kubo RPC on 127.0.0.1:10107 refuses a local
// caller without the bearer and serves one with it (docs/SECURITY.md).
func TestKubo_bearerRequired(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, node := range f.State.Nodes {
		if p := services.Kubo(t, f, node, "/api/v0/id", false); p.Status != http.StatusUnauthorized && p.Status != http.StatusForbidden {
			t.Errorf("%s: Kubo answered an unauthenticated local call with %d (exit %d)", node.Name, p.Status, p.Exit)
		}
		if p := services.Kubo(t, f, node, "/api/v0/id", true); p.Status != http.StatusOK {
			t.Errorf("%s: Kubo refused its own bearer: %d %.200s", node.Name, p.Status, p.Body)
		}
	}
}

// TestCluster_restBasicAuth: IPFS Cluster's REST API wants basic auth
// (user orama, derived password) and service.json is 0600 (docs/SECURITY.md).
func TestCluster_restBasicAuth(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, node := range f.State.Nodes {
		if p := services.ClusterREST(t, f, node, "/id", false); p.Status != http.StatusUnauthorized {
			t.Errorf("%s: cluster REST without credentials answered %d, want 401", node.Name, p.Status)
		}
		if p := services.ClusterREST(t, f, node, "/id", true); p.Status != http.StatusOK {
			t.Errorf("%s: cluster REST with the derived password answered %d", node.Name, p.Status)
		}
		if mode := strings.TrimSpace(f.MustExec(t, node, "stat -c %a "+svcJSON).Stdout); mode != "600" {
			t.Errorf("%s: service.json mode %s, want 600", node.Name, mode)
		}
	}
}

// TestCluster_kuboProxyAdmitsOnlyOrama: serve-ipfs-cluster on 10110 admits
// only sockets the orama user owns (docs/ARCHITECTURE.md; sock_diag check).
func TestCluster_kuboProxyAdmitsOnlyOrama(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, node := range f.State.Nodes {
		if p := services.ClusterKuboProxyAs(t, f, node, ownerUser); p.Status != http.StatusOK {
			t.Errorf("%s: the proxy refused the orama user: %d exit %d", node.Name, p.Status, p.Exit)
		}
		for _, user := range []string{"", "nobody"} {
			if p := services.ClusterKuboProxyAs(t, f, node, user); p.Status == http.StatusOK {
				t.Errorf("%s: the proxy served uid %q", node.Name, user)
			}
		}
	}
}

// TestIPFS_loopbackOnly: Kubo, the cluster REST API and the proxy listen on
// loopback only (docs/SECURITY.md#network-isolation).
func TestIPFS_loopbackOnly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	ports := map[int]bool{services.KuboAPIPort: true, services.ClusterRESTPort: true, services.ClusterKuboProxy: true}
	for _, node := range f.State.Nodes {
		seen := map[int]bool{}
		for _, l := range f.Listeners(t, node) {
			if !ports[l.Port] || l.Proto != "tcp" {
				continue
			}
			seen[l.Port] = true
			if l.Addr != "127.0.0.1" && l.Addr != "::1" {
				t.Errorf("%s: port %d listens on %s", node.Name, l.Port, l.Addr)
			}
		}
		if len(seen) != len(ports) {
			t.Errorf("%s: only %v of the IPFS loopback ports listen", node.Name, seen)
		}
	}
}

// TestGC_timerAndBearer: the repo GC timer is active on every node and its
// oneshot carries the Kubo bearer (docs/ARCHITECTURE.md; SECURITY.md).
func TestGC_timerAndBearer(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, node := range f.State.Nodes {
		if s := f.Unit(t, node, gcTimer); s != "active" {
			t.Errorf("%s: %s is %q", node.Name, gcTimer, s)
		}
		if c := strings.TrimSpace(f.Exec(t, node, "grep -c '^IPFS_API_AUTH=bearer:' "+gcEnv).Stdout); c != "1" {
			t.Errorf("%s: %s does not carry IPFS_API_AUTH=bearer:", node.Name, gcEnv)
		}
	}
}

// TestStorageMax_halfTheDisk: Datastore.StorageMax is 50% of the repo
// filesystem in decimal GB, at least 10GB.
func TestStorageMax_halfTheDisk(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, node := range f.State.Nodes {
		got := strings.TrimSpace(f.MustExec(t, node, `python3 -c 'import json;print(json.load(open("`+
			services.IPFSRepoPath+`/config"))["Datastore"]["StorageMax"])'`).Stdout)
		size, err := strconv.ParseFloat(strings.TrimSpace(f.MustExec(t, node, "df -B1 --output=size "+
			services.IPFSRepoPath+" | tail -1").Stdout), 64)
		if err != nil {
			t.Fatalf("%s: disk size: %v", node.Name, err)
		}
		want := math.Max(math.Floor(size*storageMaxFraction/1e9), storageMaxFloorGB)
		if got != fmt.Sprintf("%dGB", int64(want)) {
			t.Errorf("%s: StorageMax %q, want %dGB for a %.0f-byte disk", node.Name, got, int64(want), size)
		}
	}
}

// ipfsReport is the node report's IPFS section (core/pkg/telemetry/report).
type ipfsReport struct {
	DaemonActive    bool   `json:"daemon_active"`
	ClusterActive   bool   `json:"cluster_active"`
	ClusterPeers    int    `json:"cluster_peer_count"`
	HasSwarmKey     bool   `json:"has_swarm_key"`
	BootstrapEmpty  bool   `json:"bootstrap_empty"`
	RepoMaxBytes    int64  `json:"repo_max_bytes"`
	ClusterError    string `json:"cluster_error"`
	PinLockError    string `json:"pin_lock_error"`
	OldestPinLockAg int64  `json:"oldest_pin_lock_age_seconds"`
}

// TestMonitor_ipfsPlumbing: the operator's report reads Kubo and the cluster
// with their credentials, and reads the pin-lock list that feeds the
// "pin/add active" alert (docs/MONITORING.md#ipfs).
func TestMonitor_ipfsPlumbing(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	reports := services.Subsystem[ipfsReport](t, "ipfs")
	if len(reports) < len(f.State.Nodes) {
		t.Errorf("%d of %d nodes reported IPFS", len(reports), len(f.State.Nodes))
	}
	for host, r := range reports {
		if !r.DaemonActive || !r.ClusterActive || !r.HasSwarmKey || !r.BootstrapEmpty {
			t.Errorf("%s: IPFS report %+v", host, r)
		}
		if r.ClusterError != "" || r.PinLockError != "" {
			t.Errorf("%s: the collector could not authenticate: cluster %q pin-lock %q", host, r.ClusterError, r.PinLockError)
		}
		if r.ClusterPeers < len(f.State.Nodes)-1 || r.RepoMaxBytes <= 0 {
			t.Errorf("%s: %d cluster peers, repo max %d", host, r.ClusterPeers, r.RepoMaxBytes)
		}
	}
}
