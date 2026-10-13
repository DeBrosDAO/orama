package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// What the home snapshot holds and how long it is reused.
const (
	// HomeBlocks and HomeTxs are the newest blocks and transactions in the snapshot.
	HomeBlocks = 24
	HomeTxs    = 12
	// homeExamplesWindow is how many of the newest transactions the example targets are chosen from.
	homeExamplesWindow = 100
	// HeadMaxAge is how long, in seconds, an answer that depends on the chain's head may be cached by
	// a browser or a shared cache, and how long the home snapshot is reused: about a block.
	HeadMaxAge = 2
	// homeBuildTimeout bounds the chain reads one snapshot makes.
	homeBuildTimeout = 8 * time.Second

	queryBaseFee = "/orama.fees.v1.Query/BaseFee"
)

// HomeStatus is the index's position against the chain's, as /status has it, with the lag.
type HomeStatus struct {
	StartHeight    int64 `json:"start_height"`
	Cursor         int64 `json:"cursor"`
	Earliest       int64 `json:"earliest"`
	Tip            int64 `json:"tip"`
	Lag            int64 `json:"lag"`
	AggregatesFrom int64 `json:"aggregates_from"`
	// Stuck says that the indexer cannot go on and where, not why: the error behind it can name the
	// node's addresses and is for the operator, who reads it on /status.
	Stuck *HomeStuck `json:"stuck,omitempty"`
}

// HomeStuck is Stuck without its reason.
type HomeStuck struct {
	Height      int64     `json:"height"`
	StatePruned bool      `json:"state_pruned"`
	Since       time.Time `json:"since"`
	Failures    int       `json:"failures"`
}

// HomeEpoch is the epoch in progress at the chain's tip, as x/emission has it.
type HomeEpoch struct {
	Number          uint64    `json:"number"`
	StartTime       time.Time `json:"start_time"`
	DurationSeconds int64     `json:"duration_seconds"`
	EndsAt          time.Time `json:"ends_at"`
}

// HomeExamples are pages worth opening from the home page; a field is empty when nothing suitable
// is indexed.
type HomeExamples struct {
	LatestTx    string `json:"latest_tx"`
	BusyAccount string `json:"busy_account"`
}

// Home is everything the explorer's home page shows, in one answer: the position of the index, the
// newest blocks and transactions, the last 48 hours of transaction counts, and the chain's epoch in
// progress, supply and base fee at its tip.
type Home struct {
	Status HomeStatus     `json:"status"`
	Blocks []BlockSummary `json:"blocks"`
	Txs    []Tx           `json:"txs"`
	Hours  []HourStat     `json:"hours"`
	// Epoch, Supply and BaseFee are read from the chain at SupplyHeight, the tip when the snapshot was
	// built. They need no index: they are the same on a node whose index has only begun.
	Epoch        HomeEpoch    `json:"epoch"`
	Supply       string       `json:"supply"`
	BaseFee      string       `json:"base_fee"`
	SupplyHeight int64        `json:"supply_height"`
	Examples     HomeExamples `json:"examples"`
}

// chainReadError marks a failure to read the node, as against the index.
type chainReadError struct{ err error }

func (e chainReadError) Error() string { return e.err.Error() }
func (e chainReadError) Unwrap() error { return e.err }

// buildHome reads the snapshot. It makes the chain reads the index cannot answer (the tip, the
// supply and the base fee) and takes everything else from one pass over the index.
func (a *API) buildHome(ctx context.Context) ([]byte, error) {
	earliest, tip, err := a.chain.HeightRange(ctx)
	if err != nil {
		return nil, chainReadError{fmt.Errorf("failed to read the chain's tip: %w", err)}
	}
	h, err := a.store.homeFromIndex(earliest, tip)
	if err != nil {
		return nil, err
	}
	r := chainReader{ctx: ctx, chain: a.chain, height: tip}
	supply, err := r.totalSupply()
	if err != nil {
		return nil, chainReadError{fmt.Errorf("failed to read the supply for the home snapshot: %w", err)}
	}
	var fee feestypes.QueryBaseFeeResponse
	if err := r.ask(queryBaseFee, &feestypes.QueryBaseFeeRequest{}, &fee); err != nil {
		return nil, chainReadError{fmt.Errorf("failed to read the base fee for the home snapshot: %w", err)}
	}
	if h.Epoch, err = homeEpoch(r); err != nil {
		return nil, chainReadError{fmt.Errorf("failed to read the epoch for the home snapshot: %w", err)}
	}
	h.Supply, h.BaseFee, h.SupplyHeight = supply.String(), fee.BaseFee.String(), tip
	body, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the home snapshot: %w", err)
	}
	return append(body, '\n'), nil
}

// homeFromIndex is the part of the snapshot the index answers, read from one snapshot of it.
func (s *Store) homeFromIndex(earliest, tip int64) (Home, error) {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	st, err := s.statusAt(snap)
	if err != nil {
		return Home{}, err
	}
	h := Home{Status: HomeStatus{
		StartHeight: st.StartHeight, Cursor: st.Cursor, Earliest: earliest, Tip: tip,
		Lag: max(tip-st.Cursor, 0), AggregatesFrom: st.AggregatesFrom, Stuck: homeStuck(st.Stuck),
	}}
	if h.Blocks, err = blockPage(snap, 1, HomeBlocks); err != nil {
		return Home{}, err
	}
	recent, err := txPageFrom(snap, []byte(pfxLatest), listPos{page: 1}, homeExamplesWindow, "the transaction list")
	if err != nil {
		return Home{}, err
	}
	h.Txs = recent[:min(len(recent), HomeTxs)]
	h.Examples = examplesOf(recent)
	if h.Hours, err = hoursAt(snap, st.Cursor); err != nil {
		return Home{}, err
	}
	return h, nil
}

func homeStuck(s *Stuck) *HomeStuck {
	if s == nil {
		return nil
	}
	return &HomeStuck{Height: s.Height, StatePruned: s.StatePruned, Since: s.Since, Failures: s.Failures}
}

// examplesOf picks the newest transaction and the account that signed most of the window.
func examplesOf(txs []Tx) HomeExamples {
	var ex HomeExamples
	if len(txs) > 0 {
		ex.LatestTx = txs[0].Hash
	}
	counts := map[string]int{}
	for _, t := range txs {
		if t.Signer != "" {
			counts[t.Signer]++
		}
	}
	signers := make([]string, 0, len(counts))
	for s := range counts {
		signers = append(signers, s)
	}
	sort.Slice(signers, func(i, j int) bool {
		if counts[signers[i]] != counts[signers[j]] {
			return counts[signers[i]] > counts[signers[j]]
		}
		return signers[i] < signers[j]
	})
	if len(signers) > 0 {
		ex.BusyAccount = signers[0]
	}
	return ex
}

// homeEpoch reads the epoch in progress and the rule that ends it from x/emission.
func homeEpoch(r chainReader) (HomeEpoch, error) {
	state, err := r.emissionState()
	if err != nil {
		return HomeEpoch{}, err
	}
	p, err := r.emissionParams()
	if err != nil {
		return HomeEpoch{}, err
	}
	start := time.Unix(0, state.EpochStartUnixNano).UTC()
	return HomeEpoch{
		Number: state.CurrentEpoch, StartTime: start, DurationSeconds: p.EpochDurationSeconds,
		EndsAt: start.Add(time.Duration(p.EpochDurationSeconds) * time.Second),
	}, nil
}

// homeCache holds the newest snapshot and lets one request at a time build the next.
type homeCache struct {
	mu      sync.Mutex
	cursor  int64
	at      time.Time
	body    []byte
	pending *homeCall
}

// errHomeBuildAborted is what readers waiting on a build that panicked are told: the build is not
// kept, and the next request makes its own.
var errHomeBuildAborted = errors.New("the home snapshot build was aborted")

type homeCall struct {
	done chan struct{}
	body []byte
	err  error
}

// get returns the cached snapshot if it was built at the same cursor less than HeadMaxAge ago, and
// otherwise builds one: a request that finds a build under way waits for it instead of making its
// own, so a burst of readers costs one set of chain reads.
func (c *homeCache) get(ctx context.Context, cursor int64, now func() time.Time, build func(context.Context) ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if c.body != nil && c.cursor == cursor && now().Sub(c.at) < HeadMaxAge*time.Second {
		body := c.body
		c.mu.Unlock()
		return body, nil
	}
	if call := c.pending; call != nil {
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.body, call.err
		case <-ctx.Done():
			return nil, fmt.Errorf("gave up waiting for the home snapshot: %w", ctx.Err())
		}
	}
	call := &homeCall{done: make(chan struct{}), err: errHomeBuildAborted}
	c.pending = call
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.pending = nil
		if call.err == nil {
			c.cursor, c.at, c.body = cursor, now(), call.body
		}
		c.mu.Unlock()
		close(call.done)
	}()

	buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), homeBuildTimeout)
	defer cancel()
	call.body, call.err = build(buildCtx)
	return call.body, call.err
}
