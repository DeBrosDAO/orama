package report

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const (
	// chainCollectTimeout bounds the whole chain section: the unit checks and
	// every RPC query (each also bounded by httpGet's own timeout).
	chainCollectTimeout = 15 * time.Second
	// chainUnitNotFound is systemd's LoadState for a unit with no unit file:
	// this node runs no chain.
	chainUnitNotFound = "not-found"
	// chainBlockWindow is how many recent blocks the average block time spans.
	// CometBFT's /blockchain returns at most 20 block metas per call.
	chainBlockWindow = 20
	// chainValidatorsPerPage is CometBFT's largest /validators page.
	chainValidatorsPerPage = 100
	// chainMaxValidatorPages bounds the validator walk.
	chainMaxValidatorPages = 10
	// chainMaxValidators is the most validators the report keeps.
	chainMaxValidators = chainMaxValidatorPages * chainValidatorsPerPage
	// chainErrorMaxLen keeps the report's error short.
	chainErrorMaxLen = 200
)

// chainRPCBase is the CometBFT RPC the chain unit serves on loopback,
// chainNodeKeyPath the node key its answers are checked against, and
// chainSystemctl how the collector asks systemd about the unit. All are
// variables so tests can stand in for the node.
var (
	chainRPCBase     = constants.LocalChainRPCURL()
	chainNodeKeyPath = constants.ChainNodeKeyPath
	chainSystemctl   = func(ctx context.Context, args ...string) (string, error) {
		return runCmd(ctx, "systemctl", args...)
	}
)

// collectChain reports this node's view of the Orama L1. It is nil when the
// chain unit is not installed here.
func collectChain() *ChainReport {
	ctx, cancel := context.WithTimeout(context.Background(), chainCollectTimeout)
	defer cancel()

	unit := constants.ChainServiceUnit
	loadState, err := chainSystemctl(ctx, "show", "-p", "LoadState", "--value", unit)
	if err != nil {
		return &ChainReport{Error: shortChainError(fmt.Errorf("read the load state of %s: %w", unit, err))}
	}
	if loadState == chainUnitNotFound {
		return nil
	}
	// is-active exits non-zero for any state but active, so an error here is
	// the answer "not active", not a failure to ask.
	state, _ := chainSystemctl(ctx, "is-active", unit)
	r := &ChainReport{ServiceActive: state == "active"}
	nodeID, err := chainNodeID(chainNodeKeyPath)
	if err != nil {
		r.Error = shortChainError(err)
		return r
	}
	queryChainRPC(ctx, chainRPCBase, nodeID, r, time.Now())
	return r
}

// queryChainRPC fills r from the RPC at base, which must answer as the node
// whose p2p id is nodeID. Any failed query marks the RPC unresponsive and
// names the query; what was read before it is kept.
func queryChainRPC(ctx context.Context, base, nodeID string, r *ChainReport, now time.Time) {
	steps := []func() error{
		func() error { return readChainStatus(ctx, base, nodeID, r, now) },
		func() error { return readChainPeers(ctx, base, r) },
		func() error { return readChainMempool(ctx, base, r) },
		func() error { return readChainValidators(ctx, base, r) },
		func() error { return readChainBlockTimes(ctx, base, r) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			r.Responsive = false
			r.Error = shortChainError(err)
			return
		}
	}
	r.Responsive = true
}

// chainRPC GETs path from the CometBFT RPC and decodes the JSON-RPC result.
// CometBFT wraps every answer in {"jsonrpc","id","result"} or an "error".
func chainRPC(ctx context.Context, base, path string, result any) error {
	body, err := httpGet(ctx, base+path)
	if err != nil {
		return fmt.Errorf("chain RPC %s: %w", path, err)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("chain RPC %s: decode the response: %w", path, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("chain RPC %s: %s %s", path, envelope.Error.Message, envelope.Error.Data)
	}
	if len(envelope.Result) == 0 {
		return fmt.Errorf("chain RPC %s: the response has no result", path)
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("chain RPC %s: decode the result: %w", path, err)
	}
	return nil
}

// CometBFT encodes every integer as a JSON string, hence the ",string" tags.
type chainStatus struct {
	NodeInfo struct {
		ID      string `json:"id"`
		Network string `json:"network"`
		Version string `json:"version"`
	} `json:"node_info"`
	SyncInfo struct {
		LatestBlockHeight int64     `json:"latest_block_height,string"`
		LatestBlockTime   time.Time `json:"latest_block_time"`
		CatchingUp        bool      `json:"catching_up"`
	} `json:"sync_info"`
	ValidatorInfo struct {
		Address     string `json:"address"`
		VotingPower int64  `json:"voting_power,string"`
	} `json:"validator_info"`
}

func readChainStatus(ctx context.Context, base, nodeID string, r *ChainReport, now time.Time) error {
	var st chainStatus
	if err := chainRPC(ctx, base, "/status", &st); err != nil {
		return err
	}
	if err := validateChainStatus(&st, nodeID); err != nil {
		return err
	}
	r.ChainID = st.NodeInfo.Network
	r.NodeVersion = st.NodeInfo.Version
	r.LatestHeight = st.SyncInfo.LatestBlockHeight
	r.CatchingUp = st.SyncInfo.CatchingUp
	r.VotingPower = st.ValidatorInfo.VotingPower
	r.IsValidator = st.ValidatorInfo.VotingPower > 0
	// Before the first block CometBFT reports the zero time; there is no
	// block to be old yet.
	if r.LatestHeight > 0 {
		r.LatestBlockTime = st.SyncInfo.LatestBlockTime
		r.BlockAgeSec = now.Sub(st.SyncInfo.LatestBlockTime).Seconds()
	}
	return nil
}

func readChainPeers(ctx context.Context, base string, r *ChainReport) error {
	var ni struct {
		NPeers int `json:"n_peers,string"`
	}
	if err := chainRPC(ctx, base, "/net_info", &ni); err != nil {
		return err
	}
	r.Peers = ni.NPeers
	return nil
}

// readChainMempool reads the mempool size. CometBFT's /num_unconfirmed_txs
// sets n_txs and total to the same mempool size; total is the documented one.
func readChainMempool(ctx context.Context, base string, r *ChainReport) error {
	var mp struct {
		Total int `json:"total,string"`
	}
	if err := chainRPC(ctx, base, "/num_unconfirmed_txs", &mp); err != nil {
		return err
	}
	r.MempoolTxs = mp.Total
	return nil
}

// readChainValidators walks the active validator set page by page.
func readChainValidators(ctx context.Context, base string, r *ChainReport) error {
	r.Validators, r.TotalVotingPower = nil, 0
	for page := 1; page <= chainMaxValidatorPages; page++ {
		var vs struct {
			Validators []struct {
				Address     string `json:"address"`
				VotingPower int64  `json:"voting_power,string"`
			} `json:"validators"`
			Total int `json:"total,string"`
		}
		path := fmt.Sprintf("/validators?page=%d&per_page=%d", page, chainValidatorsPerPage)
		if err := chainRPC(ctx, base, path, &vs); err != nil {
			return err
		}
		if len(r.Validators)+len(vs.Validators) > chainMaxValidators {
			return fmt.Errorf("chain RPC /validators: the set is over %d members", chainMaxValidators)
		}
		for _, v := range vs.Validators {
			if err := validateValidatorAddress(v.Address); err != nil {
				return err
			}
			r.Validators = append(r.Validators, ChainValidator{Address: v.Address, VotingPower: v.VotingPower})
			r.TotalVotingPower += v.VotingPower
		}
		if len(vs.Validators) == 0 || len(r.Validators) >= vs.Total {
			return nil
		}
	}
	return fmt.Errorf("chain RPC /validators: the set is over %d members; read %d",
		chainMaxValidators, len(r.Validators))
}

type chainBlockMeta struct {
	Header struct {
		Height int64     `json:"height,string"`
		Time   time.Time `json:"time"`
	} `json:"header"`
}

// readChainBlockTimes averages the interval between the most recent blocks.
func readChainBlockTimes(ctx context.Context, base string, r *ChainReport) error {
	if r.LatestHeight < 2 {
		return nil // fewer than two blocks: no interval yet
	}
	minHeight := max(r.LatestHeight-chainBlockWindow, 1)
	var bc struct {
		BlockMetas []chainBlockMeta `json:"block_metas"`
	}
	path := fmt.Sprintf("/blockchain?minHeight=%d&maxHeight=%d", minHeight, r.LatestHeight)
	if err := chainRPC(ctx, base, path, &bc); err != nil {
		return err
	}
	r.AvgBlockTimeSec = avgBlockInterval(bc.BlockMetas)
	return nil
}

// avgBlockInterval is the mean seconds per block across metas, which
// CometBFT returns newest first. Dividing by the height span rather than the
// number of metas keeps it right if the node has pruned a block in between.
func avgBlockInterval(metas []chainBlockMeta) float64 {
	if len(metas) < 2 {
		return 0
	}
	sorted := append([]chainBlockMeta(nil), metas...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Header.Height < sorted[j].Header.Height })
	first, last := sorted[0].Header, sorted[len(sorted)-1].Header
	if last.Height <= first.Height {
		return 0
	}
	return last.Time.Sub(first.Time).Seconds() / float64(last.Height-first.Height)
}

// shortChainError keeps the report's error to one short, printable line: an
// RPC error envelope's text comes from whatever answers on the port.
func shortChainError(err error) string {
	msg := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '?'
	}, err.Error())
	if len(msg) > chainErrorMaxLen {
		return msg[:chainErrorMaxLen] + "…"
	}
	return msg
}
