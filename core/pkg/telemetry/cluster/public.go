package cluster

import (
	"sort"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// PublicStatus is what anyone may see about the network: each service's state
// and history, the chain's progress and the traffic the network carries.
//
// It deliberately names no node: no address, peer id, hostname, port or
// error text. Those were stripped from the open health endpoints in the auth
// audit, and the full per-node view stays behind operator authentication
// (/v1/operator/telemetry).
type PublicStatus struct {
	Overall    State             `json:"overall"`
	Headline   string            `json:"headline"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Nodes      PublicNodes       `json:"nodes"`
	Components []PublicComponent `json:"components"`
	Chain      *PublicChain      `json:"chain,omitempty"`
	Traffic    *PublicTraffic    `json:"traffic,omitempty"`
}

// PublicNodes counts nodes without naming them. Unknown nodes — an older
// release mid-rollout — are not in Total.
type PublicNodes struct {
	Total   int `json:"total"`
	Healthy int `json:"healthy"`
	Unknown int `json:"unknown,omitempty"`
}

// PublicComponent is one service and its recent history.
type PublicComponent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	State   State  `json:"state"`
	Summary string `json:"summary"`
	// UptimePct is the share of the recorded window the service was up, or
	// nil when nothing has been recorded yet.
	UptimePct *float64    `json:"uptime_pct"`
	History   []DayUptime `json:"history"`
}

// DayUptime is a component's uptime over one UTC day.
type DayUptime struct {
	Date      string  `json:"date"` // YYYY-MM-DD
	UptimePct float64 `json:"uptime_pct"`
	// Minutes is how many minutes of the day were sampled. A day recording
	// started part-way through weighs less in the average than a full one.
	Minutes int64 `json:"minutes"`
}

// UptimeHistory is recorded uptime by component id, oldest day first.
type UptimeHistory map[string][]DayUptime

// PublicChain is the chain's progress as the network sees it.
type PublicChain struct {
	ChainID          string            `json:"chain_id"`
	Height           int64             `json:"height"`
	BlockAgeSec      float64           `json:"block_age_sec"`
	AvgBlockTimeSec  float64           `json:"avg_block_time_sec"`
	MempoolTxs       int               `json:"mempool_txs"`
	TotalVotingPower int64             `json:"total_voting_power"`
	Validators       []PublicValidator `json:"validators"`
}

// PublicValidator is one validator's share of voting power. The consensus
// address is public on-chain data.
type PublicValidator struct {
	Address     string  `json:"address"`
	VotingPower int64   `json:"voting_power"`
	SharePct    float64 `json:"share_pct"`
}

// PublicTraffic is the whole network's request load.
type PublicTraffic struct {
	RPS       float64 `json:"rps"`
	ErrorRate float64 `json:"error_rate"`
	P95Ms     float64 `json:"p95_ms"`
}

// Public projects a snapshot onto what may be shown to anyone. Its verdict is
// the services' states alone: an alert such as a firewall rule or a stale TLS
// certificate is for operators to act on, and the public can neither see it
// nor do anything about it.
func Public(snap *ClusterSnapshot, history UptimeHistory) PublicStatus {
	components := Components(snap)
	v := serviceVerdict(snap, components)
	out := PublicStatus{
		Overall:   v.State,
		Headline:  v.Headline,
		UpdatedAt: snap.CollectedAt,
		Nodes:     PublicNodes{Total: v.NodesTotal, Healthy: v.NodesHealthy, Unknown: v.NodesUnknown},
		Chain:     publicChain(snap),
		Traffic:   publicTraffic(snap),
	}
	for _, c := range components {
		out.Components = append(out.Components, PublicComponent{
			ID: c.ID, Name: c.Name, State: c.State, Summary: c.Summary,
			UptimePct: averageUptime(history[c.ID]),
			History:   nonNil(history[c.ID]),
		})
	}
	return out
}

func nonNil(days []DayUptime) []DayUptime {
	if days == nil {
		return []DayUptime{}
	}
	return days
}

// averageUptime is the share of every sampled minute the service was up,
// so each day counts by how much of it was recorded.
func averageUptime(days []DayUptime) *float64 {
	var up float64
	var total int64
	for _, d := range days {
		up += d.UptimePct * float64(d.Minutes)
		total += d.Minutes
	}
	if total == 0 {
		return nil
	}
	avg := up / float64(total)
	return &avg
}

// publicChain takes the chain view of the node at the median height. One
// node's view cannot set what the public sees: a chain RPC a node's own
// tenant answered while the real one was down could claim any height, and
// the median of three or more stays with the honest majority.
func publicChain(snap *ClusterSnapshot) *PublicChain {
	var views []*report.ChainReport
	for _, r := range snap.Healthy() {
		if c := r.Chain; c != nil && c.Responsive {
			views = append(views, c)
		}
	}
	if len(views) == 0 {
		return nil
	}
	sort.Slice(views, func(i, j int) bool { return views[i].LatestHeight < views[j].LatestHeight })
	best := views[(len(views)-1)/2]
	pc := &PublicChain{
		ChainID: best.ChainID, Height: best.LatestHeight, BlockAgeSec: best.BlockAgeSec,
		AvgBlockTimeSec: best.AvgBlockTimeSec, MempoolTxs: best.MempoolTxs,
		TotalVotingPower: best.TotalVotingPower, Validators: []PublicValidator{},
	}
	for _, val := range best.Validators {
		share := 0.0
		if best.TotalVotingPower > 0 {
			share = float64(val.VotingPower) * 100 / float64(best.TotalVotingPower)
		}
		pc.Validators = append(pc.Validators, PublicValidator{Address: val.Address, VotingPower: val.VotingPower, SharePct: share})
	}
	sort.Slice(pc.Validators, func(i, j int) bool { return pc.Validators[i].VotingPower > pc.Validators[j].VotingPower })
	return pc
}

// publicTraffic sums request rates across nodes. Error rate is weighted by
// requests; p95 is the worst node's, a bound rather than an exact network
// percentile, which per-node histograms cannot give without shipping them.
func publicTraffic(snap *ClusterSnapshot) *PublicTraffic {
	var t PublicTraffic
	var requests, errors int64
	seen := false
	for _, r := range snap.Healthy() {
		if r.Traffic == nil {
			continue
		}
		seen = true
		t.RPS += r.Traffic.RPS
		requests += r.Traffic.Requests
		errors += r.Traffic.Errors5xx
		if r.Traffic.P95Ms > t.P95Ms {
			t.P95Ms = r.Traffic.P95Ms
		}
	}
	if !seen {
		return nil
	}
	if requests > 0 {
		t.ErrorRate = float64(errors) / float64(requests)
	}
	return &t
}
