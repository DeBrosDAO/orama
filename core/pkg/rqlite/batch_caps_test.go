package rqlite

import (
	"strings"
	"testing"
)

// TestEstimateOpResultBytes_growsWithRowCount is a sanity check that the
// estimator is monotonic in row count — required for the aggregate-bytes
// cap in BatchQuery to actually stop the OOM vector (HIGH-severity
// security finding on bugboard #270 follow-up audit).
func TestEstimateOpResultBytes_growsWithRowCount(t *testing.T) {
	row := map[string]interface{}{"id": int64(1), "name": "alice"}

	small := OpResult{Kind: BatchOpQuery, Rows: []map[string]interface{}{row}}
	big := OpResult{Kind: BatchOpQuery, Rows: make([]map[string]interface{}, 100)}
	for i := range big.Rows {
		big.Rows[i] = row
	}

	smallBytes := estimateOpResultBytes(small)
	bigBytes := estimateOpResultBytes(big)
	if bigBytes <= smallBytes {
		t.Errorf("estimator should grow with row count: 1-row=%d, 100-row=%d", smallBytes, bigBytes)
	}
	if bigBytes < smallBytes*50 {
		t.Errorf("estimator should grow ~linearly: 100×1-row=%d, 100-row=%d (expected ~100x)",
			smallBytes*100, bigBytes)
	}
}

// TestEstimateOpResultBytes_accountsForStringContent ensures the
// estimator includes the string-value bytes — otherwise large TEXT
// columns wouldn't count toward the cap and the OOM vector reopens.
func TestEstimateOpResultBytes_accountsForStringContent(t *testing.T) {
	bigString := strings.Repeat("x", 10_000)
	row := map[string]interface{}{"body": bigString}

	result := OpResult{Kind: BatchOpQuery, Rows: []map[string]interface{}{row}}
	bytes := estimateOpResultBytes(result)

	if bytes < 10_000 {
		t.Errorf("estimator must include string content bytes; got %d for a 10KB string", bytes)
	}
}

// TestEstimateOpResultBytes_emptyAndError covers edge cases that the
// aggregate-bytes loop in BatchQuery iterates over.
func TestEstimateOpResultBytes_emptyAndError(t *testing.T) {
	empty := OpResult{Kind: BatchOpQuery}
	if got := estimateOpResultBytes(empty); got <= 0 {
		t.Errorf("empty result should have non-negative estimate (got %d)", got)
	}

	withErr := OpResult{Kind: BatchOpQuery, Error: "no such table: foo"}
	if got := estimateOpResultBytes(withErr); got < len(withErr.Error) {
		t.Errorf("estimator should account for error message bytes; got %d for %d-byte error",
			got, len(withErr.Error))
	}
}

// TestMaxBatchQueryRowsPerOp_isReasonable is a sanity check — if a future
// contributor tightens the cap below typical workload sizes, this catches
// it. AnChat's read-batch case is ~10 reads × <100 rows each; we want
// plenty of headroom but not unbounded.
func TestMaxBatchQueryRowsPerOp_isReasonable(t *testing.T) {
	if MaxBatchQueryRowsPerOp < 1000 {
		t.Errorf("MaxBatchQueryRowsPerOp=%d is too low — typical reads need at least 1000 rows headroom",
			MaxBatchQueryRowsPerOp)
	}
	if MaxBatchQueryRowsPerOp > 1_000_000 {
		t.Errorf("MaxBatchQueryRowsPerOp=%d is too high — OOM vector unbounded",
			MaxBatchQueryRowsPerOp)
	}
}

// TestMaxBatchQueryTotalBytes_isReasonable mirrors above for the
// aggregate cap.
func TestMaxBatchQueryTotalBytes_isReasonable(t *testing.T) {
	if MaxBatchQueryTotalBytes < 1024*1024 {
		t.Errorf("MaxBatchQueryTotalBytes=%d is too low (< 1MB)", MaxBatchQueryTotalBytes)
	}
	if MaxBatchQueryTotalBytes > 1024*1024*1024 {
		t.Errorf("MaxBatchQueryTotalBytes=%d is too high (>1GB) — OOM vector unbounded",
			MaxBatchQueryTotalBytes)
	}
}
