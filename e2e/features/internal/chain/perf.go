//go:build e2e_fleet

package chain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// The chain-perf measurements (features/chain-perf). Block times are read from
// the headers CometBFT stored. Transaction timings run in one python3 process
// on the validator's node against its loopback RPC, so what they measure is the
// chain (CheckTx, the mempool, the block interval, the index), not the SSH
// round trip from the runner. Only a destructive package may use them: they
// sign ahead of the broadcast without the key lock (PrepareSigned).
const (
	// blockMetasPerPage is the most block headers CometBFT's /blockchain returns.
	blockMetasPerPage = 20
	// InclusionPollInterval is how often the inclusion timer asks the node for
	// the transaction; it bounds the resolution of an inclusion latency.
	InclusionPollInterval = 20 * time.Millisecond
	// InclusionTimeoutPerTx bounds the wait of one transaction for its block.
	InclusionTimeoutPerTx = 60 * time.Second
	// perfScriptBudget bounds one timing script run on a node.
	perfScriptBudget = 15 * time.Minute
	// sampleMarker starts each measurement line a timing script prints:
	// `S <start unix ns> <duration ns> <error text or ->`.
	sampleMarker = "S"
)

// BlockTimes returns the BFT times of blocks from..to (inclusive), in height
// order, as node n stored them.
func (c *Chain) BlockTimes(t testing.TB, n fleet.Node, from, to int64) []time.Time {
	t.Helper()
	var times []time.Time
	for lo := from; lo <= to; lo += blockMetasPerPage {
		hi := min(lo+blockMetasPerPage-1, to)
		var r struct {
			BlockMetas []struct {
				Header struct {
					Height Int       `json:"height"`
					Time   time.Time `json:"time"`
				} `json:"header"`
			} `json:"block_metas"`
		}
		if err := c.Comet(t, n, fmt.Sprintf("/blockchain?minHeight=%d&maxHeight=%d", lo, hi), &r); err != nil {
			t.Fatal(err)
		}
		if got, want := len(r.BlockMetas), int(hi-lo+1); got != want {
			t.Fatalf("%s: /blockchain %d..%d returned %d block headers, want %d (pruned?)", n.Name, lo, hi, got, want)
		}
		page := make([]time.Time, len(r.BlockMetas))
		for _, m := range r.BlockMetas {
			page[m.Header.Height.Int64()-lo] = m.Header.Time
		}
		times = append(times, page...)
	}
	return times
}

// BlockIntervalSamples measures the interval between consecutive blocks over
// the blocks from..to: one sample per block, started at the previous block's
// time and lasting until this block's.
func (c *Chain) BlockIntervalSamples(t testing.TB, n fleet.Node, from, to int64) []realistic.Sample {
	t.Helper()
	times := c.BlockTimes(t, n, from, to)
	samples := make([]realistic.Sample, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		samples = append(samples, realistic.Sample{At: times[i-1], Duration: times[i].Sub(times[i-1])})
	}
	return samples
}

// WaitBlocks waits until every validator is `blocks` blocks past the tip node 0
// reports now, and returns the heights the window covers.
func (c *Chain) WaitBlocks(t testing.TB, blocks int64) (from, to int64) {
	t.Helper()
	from = c.Height(t)
	to = from + blocks
	c.WaitHeight(t, to)
	return from, to
}

// timingScript runs `body` for each prepared transaction file in [from, to) of
// dir and prints one sampleMarker line per transaction. The python3 helper
// `rpc` posts JSON-RPC to the node's loopback and `get` reads a URI route.
func (c *Chain) timingScript(dir string, from, to int, body string) string {
	return fmt.Sprintf(`python3 - %s %s %d %d <<'PY'
import base64, glob, hashlib, http.client, json, sys, time
base, d, lo, hi = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
host, port = base.split("//")[1].split(":")
conn = http.client.HTTPConnection(host, int(port), timeout=30)
def rpc(method, params):
    conn.request("POST", "/", json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}), {"Content-Type": "application/json"})
    return json.loads(conn.getresponse().read())
def get(path):
    conn.request("GET", path)
    return json.loads(conn.getresponse().read())
def emit(start_ns, err):
    print("%s", start_ns, time.time_ns() - start_ns, err or "-")
%s
PY
`, fleet.ShellQuote(c.RPCHTTP()), fleet.ShellQuote(dir), from, to, sampleMarker, body)
}

// InclusionSamples broadcasts the prepared transactions [from, to) of dir one at
// a time with broadcast_tx_sync and times each from the broadcast to the moment
// the node serves it from its index as delivered with code 0.
func (c *Chain) InclusionSamples(t testing.TB, k Key, dir string, from, to int) []realistic.Sample {
	t.Helper()
	body := fmt.Sprintf(`for f in sorted(glob.glob(d + "/tx*.b64"))[lo:hi]:
    b64 = open(f).read().strip()
    digest = hashlib.sha256(base64.b64decode(b64)).hexdigest().upper()
    start = time.time_ns()
    sent = rpc("broadcast_tx_sync", {"tx": b64})
    code = (sent.get("result") or {}).get("code")
    if code != 0:
        emit(start, "CheckTx refused it: " + json.dumps(sent.get("error") or sent.get("result")).replace(" ", "_"))
        continue
    deadline = time.monotonic() + %d
    while True:
        found = get("/tx?hash=0x" + digest)
        result = found.get("result")
        if result is not None:
            emit(start, None if result["tx_result"].get("code", 0) == 0 else "delivered with code %%s" %% result["tx_result"]["code"])
            break
        if time.monotonic() > deadline:
            emit(start, "not in a block in time")
            break
        time.sleep(%f)`, int(InclusionTimeoutPerTx.Seconds()), InclusionPollInterval.Seconds())
	return c.runTiming(t, k.Node, c.timingScript(dir, from, to, body))
}

// CheckTxSamples broadcasts the prepared transactions [from, to) of dir back to
// back over one connection with broadcast_tx_sync, which returns when the node's
// CheckTx has run, and times each call.
func (c *Chain) CheckTxSamples(t testing.TB, k Key, dir string, from, to int) []realistic.Sample {
	t.Helper()
	body := `for f in sorted(glob.glob(d + "/tx*.b64"))[lo:hi]:
    start = time.time_ns()
    sent = rpc("broadcast_tx_sync", {"tx": open(f).read().strip()})
    code = (sent.get("result") or {}).get("code")
    emit(start, None if code == 0 else "CheckTx refused it: " + json.dumps(sent.get("error") or sent.get("result")).replace(" ", "_"))`
	return c.runTiming(t, k.Node, c.timingScript(dir, from, to, body))
}

// WaitSequence waits until addr's account is at sequence want on n: every
// transaction signed with a lower one is in a block.
func (c *Chain) WaitSequence(t testing.TB, n fleet.Node, addr string, want uint64) {
	t.Helper()
	eventually.Require(t, PollEvery, EpochBudget, fmt.Sprintf("%s to reach sequence %d", addr, want), func() (bool, error) {
		acc, ok := c.AccountOf(t, n, addr)
		if !ok || acc.Sequence < want {
			return false, fmt.Errorf("sequence %d", acc.Sequence)
		}
		return true, nil
	})
}

func (c *Chain) runTiming(t testing.TB, n fleet.Node, script string) []realistic.Sample {
	t.Helper()
	out := c.Run(t, n, perfScriptBudget, script)
	if out.Exit != 0 {
		t.Fatal(c.F.Redact(fmt.Sprintf("%s: the timing script exited %d: %s", n.Name, out.Exit, out.Stderr)))
	}
	samples, err := parseSamples(out.Stdout)
	if err != nil {
		t.Fatal(c.F.Redact(err.Error()))
	}
	return samples
}

// parseSamples reads the sampleMarker lines of a timing script.
func parseSamples(stdout string) ([]realistic.Sample, error) {
	var samples []realistic.Sample
	for _, line := range strings.Split(stdout, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || f[0] != sampleMarker {
			continue
		}
		start, err1 := strconv.ParseInt(f[1], 10, 64)
		dur, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || dur < 0 {
			return nil, fmt.Errorf("malformed timing line %q", line)
		}
		s := realistic.Sample{At: time.Unix(0, start), Duration: time.Duration(dur)}
		if f[3] != "-" {
			s.Err = fmt.Errorf("%s", strings.ReplaceAll(f[3], "_", " "))
		}
		samples = append(samples, s)
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("the timing script printed no sample: %q", stdout)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })
	return samples, nil
}
