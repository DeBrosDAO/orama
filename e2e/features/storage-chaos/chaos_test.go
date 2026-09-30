//go:build e2e_fleet

package storagechaos

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	ipfsUnit    = "orama-namespace-ipfs@index.service"
	clusterUnit = "orama-namespace-ipfs-cluster@index.service"
	pollEvery   = 5 * time.Second
	readBudget  = 3 * time.Minute
	// recoverBudget covers ipfs-cluster's own recovery and, failing that,
	// two runs of the 15-minute pin sweep (core/pkg/gateway/pin_sweep.go).
	recoverBudget = 35 * time.Minute
	rf            = 3
	blobBytes     = 8 << 10
)

// TestIPFSDown_contentStillServedAndRecovers stops node-3's Kubo daemon
// (the cleanup restarts it), reads pinned content through node-1 and node-2,
// uploads new content meanwhile, then restarts it and waits for node-3 to
// serve both and for both to be pinned on three peers.
func TestIPFSDown_contentStillServedAndRecovers(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	owner := tenancy.Owner(n)
	before := blob(t)
	cidBefore := uploadOK(t, n.Client, owner, "before.bin", before)
	waitPinned(t, n.Client, owner, cidBefore)
	victim := f.Node(t, "node-3")
	var cidDuring string
	var during []byte
	t.Run("daemon down", func(t *testing.T) {
		f.StopService(t, victim, ipfsUnit)
		if s := f.Unit(t, victim, clusterUnit); s == "active" {
			t.Errorf("%s stayed active with its daemon stopped (Requires=)", clusterUnit)
		}
		for _, node := range f.State.Nodes[:2] {
			waitContent(t, n.Client.PinTo(node.PublicIP), owner, cidBefore, before)
		}
		during = blob(t)
		cidDuring = uploadOK(t, n.Client.PinTo(f.State.Nodes[0].PublicIP), owner, "during.bin", during)
		waitContent(t, n.Client.PinTo(f.State.Nodes[1].PublicIP), owner, cidDuring, during)
	})
	// The subtest's cleanup restarted the daemon and waited for it.
	waitContent(t, n.Client.PinTo(victim.PublicIP), owner, cidBefore, before)
	if cidDuring != "" {
		checkRecovered(t, f, n, victim, cidDuring, during)
	}
}

// checkRecovered: the node serves what was uploaded while it was down, and the
// content returns to RF 3.
func checkRecovered(t *testing.T, f *fleet.Fleet, n *ns.Namespace, victim fleet.Node, cid string, data []byte) {
	// Only the IPFS daemon was restarted; the cluster unit recovers through the
	// node's reconcile loop, so it is waited for, not read once.
	eventually.Require(t, 5*time.Second, recoverBudget, clusterUnit+" to be active again", func() (bool, error) {
		if s := f.Unit(t, victim, clusterUnit); s != "active" {
			return false, fmt.Errorf("%s is %q", clusterUnit, s)
		}
		return true, nil
	})
	waitContent(t, n.Client.PinTo(victim.PublicIP), tenancy.Owner(n), cid, data)
	eventually.Require(t, time.Minute, recoverBudget, cid+" back at RF 3", func() (bool, error) {
		return pinnedPeers(t, n.Client, tenancy.Owner(n), cid) >= rf, nil
	})
}

func blob(t testing.TB) []byte {
	t.Helper()
	b := make([]byte, blobBytes)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func uploadOK(t testing.TB, c *gw.Client, who tenancy.Cred, name string, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var u struct{ Cid string }
	r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/storage/upload", Bearer: who.Bearer,
		Header: http.Header{"Content-Type": {w.FormDataContentType()}}, Body: buf.Bytes()})
	if err := r.Expect(t, http.StatusOK).Decode(&u); err != nil || u.Cid == "" {
		t.Fatalf("upload %s: %v %s", name, err, r.Body)
	}
	return u.Cid
}

func waitContent(t testing.TB, c *gw.Client, who tenancy.Cred, cid string, want []byte) {
	t.Helper()
	eventually.Require(t, pollEvery, readBudget, "download of "+cid+" via "+c.PinnedIP(), func() (bool, error) {
		r := tenancy.Get(t, c, "/v1/storage/get/"+cid, who)
		if r.Status != http.StatusOK {
			return false, fmt.Errorf("status %d: %.200s", r.Status, r.Body)
		}
		return bytes.Equal(r.Body, want), nil
	})
}

func pinnedPeers(t testing.TB, c *gw.Client, who tenancy.Cred, cid string) int {
	t.Helper()
	r := tenancy.Get(t, c, "/v1/storage/status/"+cid, who)
	var s struct {
		Status string   `json:"status"`
		Peers  []string `json:"peers"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &s) != nil || s.Status != "pinned" {
		return 0
	}
	return len(s.Peers)
}

func waitPinned(t testing.TB, c *gw.Client, who tenancy.Cred, cid string) {
	t.Helper()
	eventually.Require(t, pollEvery, readBudget, cid+" pinned", func() (bool, error) {
		return pinnedPeers(t, c, who, cid) >= rf, nil
	})
}
