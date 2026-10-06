package main

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/DeBrosOfficial/network/chain/client/node"

	// The modules' Query services must be registered for the invariants check to find them.
	_ "github.com/DeBrosOfficial/network/chain/x/emission/types"
	_ "github.com/DeBrosOfficial/network/chain/x/fees/types"
	_ "github.com/DeBrosOfficial/network/chain/x/houses/types"
	_ "github.com/DeBrosOfficial/network/chain/x/market/types"
	_ "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	_ "github.com/DeBrosOfficial/network/chain/x/power/types"
	_ "github.com/DeBrosOfficial/network/chain/x/relay/types"
	_ "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	_ "github.com/DeBrosOfficial/network/chain/x/storage/types"
	_ "github.com/DeBrosOfficial/network/chain/x/token/types"
)

const (
	// inclusionCommitMagic starts the injected extended-commit transaction that C13 puts first in
	// every block after the vote-extension enable height (chain/app/inclusion_commit.go,
	// injectedCommitMagic; the constant is unexported there).
	inclusionCommitMagic = "ORAMA-INCLUSION-EXTENDED-COMMIT-V1:"
	// blocksToWatch is how many new blocks the liveness check waits for.
	blocksToWatch = 3
	blocksWait    = 90 * time.Second
	blockPoll     = 2 * time.Second
)

// invariantModules are the modules whose Invariants query must hold on every node
// (docs/SECURITY_PLAYBOOKS.md); the same list deploy.sh's `invariants` command runs.
var invariantModules = []string{"emission", "fees", "storage", "nodes", "relay", "houses", "token", "market", "power", "shielded"}

// hasInclusionCommit reports whether a block's first transaction is the injected extended commit.
func hasInclusionCommit(txs [][]byte) bool {
	return len(txs) > 0 && bytes.HasPrefix(txs[0], []byte(inclusionCommitMagic))
}

// advanced reports whether the chain moved at least want blocks from first to last.
func advanced(first, last, want int64) bool { return last-first >= want }

// checkBlocks: blocks advance on every node, and a block after height 3 carries the injected
// inclusion-list commit.
func checkBlocks(ctx context.Context, e *env) []Result {
	var out []Result
	first := e.nodes[0]
	c, err := e.client(ctx, first)
	if err != nil {
		return []Result{fail("blocks", "%v", err)}
	}
	start, err := c.LatestHeight(ctx)
	if err != nil {
		return []Result{fail("blocks", "read the height of %s: %v", first.Name, err)}
	}
	deadline := time.Now().Add(blocksWait)
	last := start
	for time.Now().Before(deadline) && !advanced(start, last, blocksToWatch) {
		select {
		case <-ctx.Done():
			return []Result{fail("blocks", "%v", ctx.Err())}
		case <-time.After(blockPoll):
		}
		if last, err = c.LatestHeight(ctx); err != nil {
			return []Result{fail("blocks", "read the height of %s: %v", first.Name, err)}
		}
	}
	if !advanced(start, last, blocksToWatch) {
		return []Result{fail("blocks", "height %d -> %d in %s: the chain is not producing blocks", start, last, blocksWait)}
	}
	out = append(out, pass("blocks", "height %d -> %d on %s", start, last, first.Name))
	return append(out, checkInclusion(ctx, c, last, e.voteExtHeight))
}

// checkInclusion reads block minInclusionHeight and the tip and requires each to start with the
// injected inclusion-list commit.
func checkInclusion(ctx context.Context, c *node.Client, tip, voteExtHeight int64) Result {
	// Blocks after the enable height carry the injected commit.
	minInclusionHeight := voteExtHeight + 1
	const name = "inclusion-list"
	if tip < minInclusionHeight {
		return fail(name, "tip %d is below height %d, which must carry the injected commit", tip, minInclusionHeight)
	}
	for _, h := range []int64{minInclusionHeight, tip} {
		blk, err := c.Block(ctx, h)
		if err != nil {
			return fail(name, "read block %d: %v", h, err)
		}
		txs := make([][]byte, len(blk.Block.Txs))
		for i, t := range blk.Block.Txs {
			txs[i] = t
		}
		if !hasInclusionCommit(txs) {
			return fail(name, "block %d (%d txs) does not start with the injected extended-commit transaction; vote extensions are not enabled at height 2", h, len(txs))
		}
	}
	return pass(name, "blocks %d and %d start with the injected extended commit", minInclusionHeight, tip)
}

// Invariant queries.

// invariantBools decodes one module's Invariants response into its boolean fields.
func invariantBools(module string, value []byte) (map[string]bool, error) {
	svc, err := gogoproto.HybridResolver.FindDescriptorByName(protoreflect.FullName(fmt.Sprintf("orama.%s.v1.Query", module)))
	if err != nil {
		return nil, fmt.Errorf("no Query service for module %s: %w", module, err)
	}
	sd, ok := svc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("orama.%s.v1.Query is not a service", module)
	}
	md := sd.Methods().ByName("Invariants")
	if md == nil {
		return nil, fmt.Errorf("module %s has no Invariants query", module)
	}
	msg := dynamicpb.NewMessage(md.Output())
	if err := proto.Unmarshal(value, msg); err != nil {
		return nil, fmt.Errorf("decode the %s invariants: %w", module, err)
	}
	out := map[string]bool{}
	fields := md.Output().Fields()
	for i := 0; i < fields.Len(); i++ {
		if f := fields.Get(i); f.Kind() == protoreflect.BoolKind {
			out[string(f.Name())] = msg.Get(f).Bool()
		}
	}
	return out, nil
}

// invariantsHold: a module with no boolean check proves nothing, so it does not hold; every check
// must be true.
func invariantsHold(checks map[string]bool) (bool, []string) {
	var broken []string
	for name, ok := range checks {
		if !ok {
			broken = append(broken, name)
		}
	}
	sort.Strings(broken)
	return len(checks) > 0 && len(broken) == 0, broken
}

// checkInvariants runs every module's Invariants query on every node.
func checkInvariants(ctx context.Context, e *env) []Result {
	var bad []string
	total := 0
	for _, n := range e.nodes {
		rc, err := e.rawClient(ctx, n)
		if err != nil {
			return []Result{fail("invariants", "%v", err)}
		}
		for _, m := range invariantModules {
			total++
			res, err := rc.ABCIQuery(ctx, fmt.Sprintf("/orama.%s.v1.Query/Invariants", m), nil)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s/%s: %v", n.Name, m, err))
				continue
			}
			if res.Response.Code != abci.CodeTypeOK {
				bad = append(bad, fmt.Sprintf("%s/%s: query code %d: %s", n.Name, m, res.Response.Code, res.Response.Log))
				continue
			}
			checks, err := invariantBools(m, res.Response.Value)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s/%s: %v", n.Name, m, err))
				continue
			}
			if ok, broken := invariantsHold(checks); !ok {
				bad = append(bad, fmt.Sprintf("%s/%s: broken %v (checks %d)", n.Name, m, broken, len(checks)))
			}
		}
	}
	if len(bad) > 0 {
		return []Result{fail("invariants", "%d of %d module checks failed: %v", len(bad), total, bad)}
	}
	return []Result{pass("invariants", "%d modules on %d nodes hold", len(invariantModules), len(e.nodes))}
}
