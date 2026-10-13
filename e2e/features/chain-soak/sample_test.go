//go:build e2e_fleet

package chainsoak

import (
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/chainsoak"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// indexerUnit and indexerHome are the chain indexer that runs beside every validator
	// (e2e/scripts/chain-deploy.sh).
	indexerUnit = "orama-global-indexer.service"
	indexerHome = "/var/lib/orama-global/indexer"
	// duBudget bounds measuring a state directory.
	duBudget = 5 * time.Minute
)

// watched is a daemon beside the validator whose memory, restarts and disk the soak follows.
type watched struct {
	unit string
	dir  string
}

var daemons = []watched{
	{unit: chain.Unit, dir: chain.Home + "/data"},
	{unit: indexerUnit, dir: indexerHome},
}

// sample takes one look at every validator: heights and the hashes of the block all of them have, the
// mempool, each daemon's memory, restarts and disk, and how far the indexer is behind.
func (r *soak) sample(t testing.TB, sent int) chainsoak.Sample {
	t.Helper()
	nodes := r.c.Nodes()
	statuses := make([]chain.Status, len(nodes))
	agreed := int64(0)
	for i, n := range nodes {
		statuses[i] = r.c.MustStatus(t, n)
		if i == 0 || statuses[i].Height-1 < agreed {
			agreed = statuses[i].Height - 1
		}
	}
	now := time.Now()
	out := chainsoak.Sample{At: now, Elapsed: now.Sub(r.start), AgreedHeight: agreed, Sent: sent}
	for i, n := range nodes {
		out.Nodes = append(out.Nodes, r.nodeSample(t, n, statuses[i], agreed))
	}
	return out
}

func (r *soak) nodeSample(t testing.TB, n fleet.Node, st chain.Status, agreed int64) chainsoak.Node {
	t.Helper()
	hdr, err := r.c.BlockHeader(t, n, agreed)
	if err != nil {
		t.Fatalf("%s: the block header at height %d: %v", n.Name, agreed, err)
	}
	node := chainsoak.Node{Name: n.Name, Height: st.Height, CatchingUp: st.CatchingUp, BlockHash: hdr.BlockHash, AppHash: hdr.AppHash,
		MempoolTxs: r.mempool(t, n), Services: map[string]chainsoak.Service{}}
	for _, d := range daemons {
		node.Services[d.unit] = r.service(t, n, d)
	}
	node.IndexerCursor, node.IndexerLag = r.indexer(t, n)
	return node
}

func (r *soak) mempool(t testing.TB, n fleet.Node) int {
	t.Helper()
	var res struct {
		N chain.Int `json:"n_txs"`
	}
	if err := r.c.Comet(t, n, "/num_unconfirmed_txs", &res); err != nil {
		t.Fatal(err)
	}
	return int(res.N.Int64())
}

func (r *soak) service(t testing.TB, n fleet.Node, d watched) chainsoak.Service {
	t.Helper()
	f := r.c.F
	restarts, err := strconv.Atoi(strings.TrimSpace(f.MustExec(t, n, "systemctl show -p NRestarts --value "+d.unit).Stdout))
	if err != nil {
		t.Fatalf("%s: NRestarts of %s: %v", n.Name, d.unit, err)
	}
	out := r.c.Run(t, n, duBudget, "sudo -n du -sb "+fleet.ShellQuote(d.dir)+" | cut -f1")
	size, err := strconv.ParseInt(strings.TrimSpace(out.Stdout), 10, 64)
	if out.Exit != 0 || err != nil {
		t.Fatalf("%s: the size of %s (exit %d): %v %s", n.Name, d.dir, out.Exit, err, out.Stderr)
	}
	return chainsoak.Service{AnonMB: realistic.AnonMemoryMB(t, f, n, d.unit), Restarts: restarts, DataBytes: size}
}

// indexer reads the node's indexer: the last height it has indexed and how far that is from the tip it
// sees.
func (r *soak) indexer(t testing.TB, n fleet.Node) (cursor, lag int64) {
	t.Helper()
	url := fmt.Sprintf("http://%s:%d/index/v1/status", r.c.Host(), constants.GlobalIndexerPort)
	out := r.c.Run(t, n, chain.QueryBudget, "curl -sS --max-time 10 "+fleet.ShellQuote(url))
	var st struct {
		Cursor int64 `json:"cursor"`
		Tip    int64 `json:"tip"`
	}
	if out.Exit != 0 || json.Unmarshal([]byte(out.Stdout), &st) != nil {
		t.Fatalf("%s: the indexer's status (exit %d): %s %s", n.Name, out.Exit, out.Stdout, out.Stderr)
	}
	return st.Cursor, max(st.Tip-st.Cursor, 0)
}
