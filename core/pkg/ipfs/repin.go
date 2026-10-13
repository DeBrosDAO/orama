package ipfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Pin re-allocation.
//
// Pin fixes replication-min and replication-max to the replication factor at
// pin time and nothing ever revisits the allocation. So when a node holding
// replicas is discarded, every CID it held drops below RF and stays there: the
// cluster does not re-allocate on its own, and a node that joins later receives
// nothing. Function WASM is re-pinned at gateway start; general storage content
// had no equivalent.
//
// Re-issuing the pin is what forces ipfs-cluster to re-run allocation against
// the peers that are actually alive.

// UnderReplicated reports whether this pin holds fewer replicas than rf.
//
// PinnedPeers counts only peers reporting "pinned". A peer that is "pinning",
// "queued" or in error has not got the content, and counting those as replicas
// is how a CID one failure away from loss reads as healthy — the aggregation
// in PinStatus() is already careful about this, and the sweep inherits it.
func underReplicated(p *PinStatus, rf int) bool { return p.PinnedPeers < rf }

// pinListEntry is the minimum of ipfs-cluster's /pins listing this needs: the
// per-CID status is fetched individually so the sweep reuses the careful
// aggregation in PinStatus() rather than repeating it.
type pinListEntry struct {
	Cid  string `json:"cid"`
	Name string `json:"name"`
}

// ListPinnedCIDs returns every CID ipfs-cluster is tracking.
func (c *Client) ListPinnedCIDs(ctx context.Context) ([]pinListEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL+"/pins", nil)
	if err != nil {
		return nil, fmt.Errorf("build pins request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list pins: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list pins failed with status %d: %s", resp.StatusCode, string(body))
	}

	// ipfs-cluster streams /pins as newline-delimited JSON objects rather than
	// one array, so it is decoded as a stream.
	var pins []pinListEntry
	dec := json.NewDecoder(resp.Body)
	for {
		var pin pinListEntry
		if err := dec.Decode(&pin); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode pins stream: %w", err)
		}
		if pin.Cid != "" {
			pins = append(pins, pin)
		}
	}
	return pins, nil
}

// RepinResult reports what a sweep did.
type RepinResult struct {
	Examined        int
	UnderReplicated int
	Repinned        int
	Failed          int
}

// RepinUnderReplicated re-issues the pin for every CID holding fewer than rf
// replicas, which forces ipfs-cluster to re-allocate onto peers that are alive.
//
// Healthy CIDs are left alone: re-pinning everything on every sweep would churn
// allocations across the whole cluster for no reason.
func (c *Client) RepinUnderReplicated(ctx context.Context, rf int) (RepinResult, error) {
	var result RepinResult

	pins, err := c.ListPinnedCIDs(ctx)
	if err != nil {
		return result, err
	}
	result.Examined = len(pins)

	for _, pin := range pins {
		status, err := c.PinStatus(ctx, pin.Cid)
		if err != nil {
			// Cannot tell how many replicas it has, so re-pinning would be a
			// guess. Count it and move on; the next sweep tries again.
			result.Failed++
			continue
		}
		if !underReplicated(status, rf) {
			continue
		}
		result.UnderReplicated++

		pinCtx, cancel := context.WithTimeout(ctx, repinTimeout)
		_, err = c.Pin(pinCtx, pin.Cid, pin.Name, rf)
		cancel()

		if err != nil {
			// One CID failing must not stop the sweep: the next one may be the
			// one that is a single failure from being lost.
			result.Failed++
			continue
		}
		result.Repinned++
	}
	return result, nil
}

// repinTimeout bounds one re-pin. Re-allocation is a cluster consensus write
// plus a transfer, so it is generous; the point is that a wedged peer cannot
// stall the whole sweep.
const repinTimeout = 60 * time.Second
