// Package report is what a run leaves behind: a JSON document with every number, and a summary a
// person reads. Both come from the same Report value; the summary invents nothing the JSON lacks.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/apphash"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/metrics"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
)

// Tool names the producer in the JSON.
const Tool = "oramad-bench"

// Step modes.
const (
	ModeOpen   = "open-loop"
	ModeClosed = "closed-loop"
)

// Report is one run.
type Report struct {
	Tool        string         `json:"tool"`
	StartedAt   time.Time      `json:"started_at"`
	FinishedAt  time.Time      `json:"finished_at"`
	Profile     string         `json:"profile"`
	Environment Environment    `json:"environment"`
	Chain       Chain          `json:"chain"`
	Types       []Type         `json:"types"`
	AppHash     apphash.Result `json:"app_hash"`
	// AppHashAgreed is false when any validator computed a different state at any height of the
	// run; every number in the report is then beside the point.
	AppHashAgreed bool     `json:"app_hash_agreed"`
	Warnings      []string `json:"warnings,omitempty"`
}

// Environment is where the numbers were measured. The nodes and the load generator share one
// machine, so the numbers are of that machine, not of a network.
type Environment struct {
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	CPUs      int    `json:"cpus"`
	Note      string `json:"note"`
}

// Chain is the chain under test.
type Chain struct {
	ChainID    string   `json:"chain_id"`
	Nodes      []string `json:"nodes"`
	Validators int      `json:"validators"`
	// VoteExtensionsEnableHeight is the consensus parameter; 0 means vote extensions are off.
	VoteExtensionsEnableHeight int64   `json:"vote_extensions_enable_height"`
	VoteExtensionsOn           bool    `json:"vote_extensions_on"`
	BlockMaxGas                int64   `json:"block_max_gas"`
	StartHeight                int64   `json:"start_height"`
	EndHeight                  int64   `json:"end_height"`
	BroadcastMode              string  `json:"broadcast_mode"`
	FeeMultiple                int64   `json:"fee_multiple"`
	Accounts                   int     `json:"accounts"`
	BlockIntervalSeconds       float64 `json:"block_interval_seconds"`
}

// Type is the result for one transaction type.
type Type struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Skipped is set when the type could not run; Reason says why. A skipped type has no steps.
	Skipped    bool       `json:"skipped"`
	Reason     string     `json:"reason,omitempty"`
	GasLimit   uint64     `json:"gas_limit,omitempty"`
	GasPrice   string     `json:"gas_price,omitempty"`
	Steps      []Step     `json:"steps,omitempty"`
	Saturation Saturation `json:"saturation"`
}

// Step is one open-loop rate or one closed-loop window.
type Step struct {
	Mode            string  `json:"mode"`
	OfferedRate     float64 `json:"offered_rate,omitempty"`
	Window          int     `json:"window,omitempty"`
	DurationSeconds float64 `json:"duration_seconds"`

	Offered       int `json:"offered"`
	Included      int `json:"included"`
	FailedInBlock int `json:"failed_in_block"`
	Rejected      int `json:"rejected"`
	NotSent       int `json:"not_sent"`
	// Unresolved are transactions that were neither included nor refused when the step ended.
	Unresolved int `json:"unresolved"`

	// IncludedInWindow counts transactions included while the step was still sending.
	IncludedInWindow int `json:"included_in_window"`
	// AchievedRate is IncludedInWindow over the send window: transactions per second the chain
	// took while the load was on.
	AchievedRate float64 `json:"achieved_rate"`
	// Backlog is how many transactions were sent and not yet included when the sending stopped.
	Backlog int `json:"backlog"`

	// Latency is the inclusion latency, in seconds, of every transaction the step got included.
	Latency metrics.Summary `json:"latency_seconds"`

	RejectionReasons map[string]int `json:"rejection_reasons,omitempty"`
	FailureReasons   map[string]int `json:"failure_reasons,omitempty"`

	Blocks         BlockSummary          `json:"blocks"`
	MempoolPeak    int                   `json:"mempool_peak"`
	Mempool        []watch.MempoolSample `json:"mempool"`
	BaseFee        BaseFee               `json:"base_fee"`
	SenderQueueMax int                   `json:"sender_queue_max"`
	// SenderBehind is set when the load generator's own queue grew past SenderBehindDepth: its
	// workers could not send as fast as the step asked, so the chain was not the only limit.
	SenderBehind bool `json:"sender_behind"`
}

// BlockSummary describes the blocks produced during a step.
type BlockSummary struct {
	Count            int     `json:"count"`
	IntervalSeconds  float64 `json:"interval_seconds"`
	AvgTxs           float64 `json:"avg_txs"`
	MaxTxs           int     `json:"max_txs"`
	AvgGasUsed       float64 `json:"avg_gas_used"`
	AvgGasWanted     float64 `json:"avg_gas_wanted"`
	MaxGasWanted     int64   `json:"max_gas_wanted"`
	GasWantedOfLimit float64 `json:"gas_wanted_of_limit"`
}

// BaseFee is what x/fees' base fee did during a step.
type BaseFee struct {
	Start   string            `json:"start"`
	End     string            `json:"end"`
	Max     string            `json:"max"`
	Samples []watch.FeeSample `json:"samples"`
}

// Saturation is where a type stopped scaling.
type Saturation struct {
	// OpenMaxSustained is the highest offered rate (tx/s) the chain kept up with: at least
	// SustainedFraction of the offered rate included while the load was on, and at most
	// MaxFailedFraction of the transactions refused or failed.
	OpenMaxSustained float64 `json:"open_max_sustained,omitempty"`
	// OpenSaturatedAt is the lowest offered rate that was not sustained (0: every rate was).
	OpenSaturatedAt float64 `json:"open_saturated_at,omitempty"`
	// ClosedMaxRate is the best AchievedRate of any closed-loop window, and ClosedBestWindow the
	// window that reached it.
	ClosedMaxRate    float64 `json:"closed_max_rate,omitempty"`
	ClosedBestWindow int     `json:"closed_best_window,omitempty"`
	// Inconclusive counts the steps left out because they spanned too few blocks to judge.
	Inconclusive int `json:"inconclusive_steps,omitempty"`
}

// WriteJSON writes the report to path, creating its directory.
func (r Report) WriteJSON(path string) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create the report directory: %w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write the report: %w", err)
	}
	return nil
}
