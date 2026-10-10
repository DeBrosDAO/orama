package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/netclass"
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

// chainNodeKeyPath is the node key the chain's answers are checked against, and chainSystemctl how
// the collector asks systemd about the unit. Both are variables so tests can stand in for the
// node.
var (
	chainNodeKeyPath = constants.ChainNodeKeyPath
	// commandStdout keeps is-active's text when systemd exits non-zero.
	// runCmd would drop "failed" and the report could not tell it from a miss.
	chainSystemctl = func(ctx context.Context, args ...string) (string, error) {
		return commandStdout(ctx, "systemctl", args...)
	}
)

// colocatedGlobal reports whether the orama-global network namespace layout is installed on this
// machine, so the global services listen on the namespace address instead of loopback.
func colocatedGlobal() bool {
	return globalnetns.Installed(constants.SystemdUnitDir, func(p string) bool { _, err := os.Stat(p); return err == nil })
}

// chainEndpoints returns the CometBFT RPC and the REST API the chain unit serves on this machine:
// loopback, or on a co-located machine (the orama-global network namespace layout is installed)
// the namespace address. It is read for each collection, not once when the process starts, so a
// co-located install made while this process runs is seen by its next report. It is a variable so
// tests can stand in for the node.
var chainEndpoints = func() (rpcBase, apiBase string) {
	if colocatedGlobal() {
		return constants.ColocatedChainRPCURL(), constants.ColocatedChainAPIURL()
	}
	return constants.LocalChainRPCURL(), constants.LocalChainAPIURL()
}

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
	// is-active exits non-zero for any state but active. commandStdout still
	// returns the printed state; an error with no text is "not active".
	state, err := chainSystemctl(ctx, "is-active", unit)
	if err != nil || state == "" {
		state = "inactive"
	}
	r := &ChainReport{ServiceActive: state == "active", UnitState: state}
	nodeID, err := chainNodeID(chainNodeKeyPath)
	if err != nil {
		r.Error = shortChainError(err)
		return r
	}
	rpcBase, apiBase := chainEndpoints()
	queryChainRPC(ctx, rpcBase, nodeID, r, time.Now())
	if r.Responsive {
		readChainSigning(r, apiBase)
	}
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
	return decodeChainResult(body, path, result)
}

func decodeChainResult(body []byte, path string, result any) error {
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

// FillChainView reads CometBFT HTTP bodies (JSON-RPC envelopes). It does not
// check the node key; the node report's collector does that before it keeps
// a height. An empty body is an RPC that did not answer.
func FillChainView(statusBody, netBody, validatorsBody []byte, now time.Time) *ChainReport {
	r := &ChainReport{}
	if len(bytes.TrimSpace(statusBody)) == 0 {
		r.Error = "chain RPC did not answer"
		return r
	}
	var st chainStatus
	if err := decodeChainResult(statusBody, "/status", &st); err != nil {
		r.Error = shortChainError(err)
		return r
	}
	if !netclass.ValidChainID(st.NodeInfo.Network) {
		r.Error = "chain RPC /status: node_info.network is not a well-formed chain id"
		return r
	}
	r.ChainID = st.NodeInfo.Network
	r.NodeVersion = st.NodeInfo.Version
	r.LatestHeight = st.SyncInfo.LatestBlockHeight
	r.CatchingUp = st.SyncInfo.CatchingUp
	r.VotingPower = st.ValidatorInfo.VotingPower
	r.IsValidator = st.ValidatorInfo.VotingPower > 0
	if chainValidatorAddressRe.MatchString(st.ValidatorInfo.Address) {
		r.ConsAddress = st.ValidatorInfo.Address
	}
	if r.LatestHeight > 0 && !st.SyncInfo.LatestBlockTime.IsZero() {
		r.LatestBlockTime = st.SyncInfo.LatestBlockTime
		r.BlockAgeSec = now.Sub(st.SyncInfo.LatestBlockTime).Seconds()
	}
	if len(bytes.TrimSpace(netBody)) == 0 {
		r.Error = "chain RPC /net_info did not answer"
		return r
	}
	var peers struct {
		NPeers int `json:"n_peers,string"`
	}
	if err := decodeChainResult(netBody, "/net_info", &peers); err != nil {
		r.Error = shortChainError(err)
		return r
	}
	r.Peers = peers.NPeers
	if len(bytes.TrimSpace(validatorsBody)) == 0 {
		r.Error = "chain RPC /validators did not answer"
		return r
	}
	var vs struct {
		Total int `json:"total,string"`
	}
	if err := decodeChainResult(validatorsBody, "/validators", &vs); err != nil {
		r.Error = shortChainError(err)
		return r
	}
	r.ValidatorCount = vs.Total
	r.Responsive = true
	return r
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
	if chainValidatorAddressRe.MatchString(st.ValidatorInfo.Address) {
		r.ConsAddress = st.ValidatorInfo.Address
	}
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
		r.ValidatorCount = vs.Total
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
