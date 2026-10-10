package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/client/node"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

const (
	archiveName = "archive"
	// archiveRangeBlocks is the width every archiver uses: orama-global archiver's default
	// (--range-blocks), which its unit does not change. The first range is 1..archiveRangeBlocks.
	archiveRangeBlocks = 1000
	// archiveAttesters is how many distinct operators must attest a range (docs/whitepaper/technical-reference/vol2/42-archive-and-indexer.md, "History
	// archiver").
	archiveAttesters = 3
)

// archiveDeal is one ARCHIVE-class deal and how many of its slots have a provider.
type archiveDeal struct {
	ID       uint64
	Assigned int
	Replicas int
}

// archiveInput is what the verdict is judged on, read from chain state.
type archiveInput struct {
	Tip        int64
	Width      int64
	RangeFound bool
	Operators  []string
	Archived   bool
	Deals      []archiveDeal
	// ProviderASNs are the declared ASNs of the registered nodes that can hold a slot; 0 is undeclared.
	ProviderASNs []uint32
	Replicas     int
}

// distinctASNs counts the different declared ASNs.
func distinctASNs(asns []uint32) int {
	seen := map[uint32]bool{}
	for _, a := range asns {
		if a != 0 {
			seen[a] = true
		}
	}
	return len(seen)
}

func distinctStrings(in []string) int {
	seen := map[string]bool{}
	for _, s := range in {
		seen[s] = true
	}
	return len(seen)
}

// archiveVerdict judges the first archive range. A protocol deal (and so an ARCHIVE deal) gives a
// slot only to a node with a declared ASN distinct from the other slots' (docs/whitepaper/technical-reference/vol2/37-global-nodes.md, "Node
// network identity"), so on an environment where every provider shares one ASN the deals stay
// unassigned. That is detected from the chain's own state, not assumed, and reported as a SKIP
// naming the cause; an archived range is a PASS whatever the ASNs are.
func archiveVerdict(in archiveInput) Result {
	if in.Tip < in.Width {
		return skip(archiveName, "the chain is at height %d and the first range ends at %d; run again once it is past it", in.Tip, in.Width)
	}
	if !in.RangeFound {
		return fail(archiveName, "range 1-%d is final (tip %d) but no archiver attested it", in.Width, in.Tip)
	}
	if n := distinctStrings(in.Operators); n < archiveAttesters {
		return fail(archiveName, "range 1-%d has no tuple attested by %d operators: the best is attested by %d operators", in.Width, archiveAttesters, n)
	}
	if in.Archived {
		return pass(archiveName, "range 1-%d attested by %d operators and archived", in.Width, distinctStrings(in.Operators))
	}
	if len(in.Deals) == 0 {
		return fail(archiveName, "range 1-%d is attested by %d operators but no ARCHIVE deal was opened", in.Width, distinctStrings(in.Operators))
	}
	unassigned := 0
	for _, d := range in.Deals {
		if d.Assigned < d.Replicas {
			unassigned++
		}
	}
	if unassigned > 0 && distinctASNs(in.ProviderASNs) < in.Replicas {
		return skip(archiveName, "environment: %d of %d ARCHIVE deals have unassigned slots and the providers declare %d distinct ASN(s) (%v); protocol deals need %d distinct ASNs. Attested by %d operators",
			unassigned, len(in.Deals), distinctASNs(in.ProviderASNs), in.ProviderASNs, in.Replicas, distinctStrings(in.Operators))
	}
	return fail(archiveName, "range 1-%d is attested but not archived: %d ARCHIVE deals, %d with unassigned slots", in.Width, len(in.Deals), unassigned)
}

// bestAttesters is the operators of the tuple that won the range, or of the candidate tuple with the
// most operators while none has won.
func bestAttesters(rec archivetypes.RangeRecord) []string {
	if rec.Decided {
		return rec.Operators
	}
	var best []string
	for _, c := range rec.Candidates {
		if len(c.Operators) > len(best) {
			best = c.Operators
		}
	}
	return best
}

// checkArchive reads the archive state from the first node and judges it.
func checkArchive(ctx context.Context, e *env) Result {
	c, err := e.client(ctx, e.nodes[0])
	if err != nil {
		return fail(archiveName, "%v", err)
	}
	in := archiveInput{Width: archiveRangeBlocks, Replicas: privateReplicas}
	if in.Tip, err = c.LatestHeight(ctx); err != nil {
		return fail(archiveName, "read the height: %v", err)
	}
	var rng archivetypes.QueryRangeResponse
	err = c.Query(ctx, "/orama.archive.v1.Query/Range", &archivetypes.QueryRangeRequest{StartHeight: 1, EndHeight: archiveRangeBlocks}, &rng)
	var qerr *node.QueryError
	switch {
	case err == nil:
		in.RangeFound, in.Operators, in.Archived = true, bestAttesters(rng.Range), rng.Range.Archived
	case errors.As(err, &qerr) && qerr.NotFound():
	default:
		return fail(archiveName, "query range 1-%d: %v", archiveRangeBlocks, err)
	}
	if in.Deals, err = archiveDeals(ctx, c); err != nil {
		return fail(archiveName, "%v", err)
	}
	if in.ProviderASNs, err = e.providerASNs(ctx, c); err != nil {
		return fail(archiveName, "%v", err)
	}
	return archiveVerdict(in)
}

// archiveDeals scans the deals for the ARCHIVE class and counts each one's assigned slots.
func archiveDeals(ctx context.Context, c *node.Client) ([]archiveDeal, error) {
	var out []archiveDeal
	for id := uint64(1); id <= dealScanLimit; id++ {
		var resp storagetypes.QueryDealResponse
		err := c.Query(ctx, "/orama.storage.v1.Query/Deal", &storagetypes.QueryDealRequest{DealId: id}, &resp)
		var qerr *node.QueryError
		if errors.As(err, &qerr) && qerr.NotFound() {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read deal %d: %w", id, err)
		}
		if resp.Deal.Class != storagetypes.DealClass_DEAL_CLASS_ARCHIVE {
			continue
		}
		d := archiveDeal{ID: id, Replicas: int(resp.Deal.Replicas)}
		for i := uint32(0); i < resp.Deal.Replicas; i++ {
			var s storagetypes.QuerySlotResponse
			if err := c.Query(ctx, "/orama.storage.v1.Query/Slot", &storagetypes.QuerySlotRequest{DealId: id, Slot: i}, &s); err != nil {
				return nil, fmt.Errorf("read slot %d of deal %d: %w", i, id, err)
			}
			if s.Slot.NodeId != "" {
				d.Assigned++
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// providerASNs are the declared ASNs of the registered stagenet nodes.
func (e *env) providerASNs(ctx context.Context, c *node.Client) ([]uint32, error) {
	var asns []uint32
	for _, n := range e.nodes {
		var resp nodestypes.QueryNodeResponse
		if err := c.Query(ctx, "/orama.nodes.v1.Query/Node", &nodestypes.QueryNodeRequest{NodeId: nodeID(n.Name)}, &resp); err != nil {
			return nil, fmt.Errorf("look up node %s: %w", nodeID(n.Name), err)
		}
		asns = append(asns, resp.Node.Asn)
	}
	return asns, nil
}
