package archiver

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// DefaultRangeBlocks is the width of one archived range. x/archive refuses
// overlapping ranges, so every archiver of a chain must use the same width
// and the ranges start at 1, 1+w, 1+2w, and so on.
const DefaultRangeBlocks = 1000

// ErrRootConflict is a range another attester pinned with a different root
// or bundle than the blocks this node reads. The runner keeps its own bundle,
// writes conflicts/<start>-<end>.json, reports the error once, and moves on:
// x/archive pins the first attestation, and one wrong attester must not stop
// every honest archiver.
var ErrRootConflict = errors.New("range is pinned on chain with a different root or bundle")

// Chain is the node the archiver reads blocks from and attests to.
type Chain interface {
	LatestHeight(ctx context.Context) (int64, error)
	Block(ctx context.Context, height int64) (Block, error)
	Range(ctx context.Context, start, end int64) (types.RangeRecord, bool, error)
	Submit(ctx context.Context, msgs ...sdk.Msg) error
	// CreateArchiveDeal submits MsgCreateArchiveDeal and returns the id of the deal it opened.
	CreateArchiveDeal(ctx context.Context, msg *types.MsgCreateArchiveDeal) (uint64, error)
	// DealStatus reads an x/storage deal. found is false for a deal that does not exist.
	DealStatus(ctx context.Context, dealID uint64) (status storagetypes.DealStatus, found bool, err error)
	// LastArchivedHeight is x/archive's contiguous archived prefix, the height below which the
	// chain lets a node prune.
	LastArchivedHeight(ctx context.Context) (int64, error)
	// DealSlots reads every slot of an x/storage deal. A slot the chain has not created yet is
	// left out.
	DealSlots(ctx context.Context, dealID uint64) ([]storagetypes.Slot, error)
	// ProviderURL is the http(s) provider root a node registered in x/nodes.
	ProviderURL(ctx context.Context, nodeID string) (string, error)
}

// Runner packs finalised ranges into bundle files, attests them, and opens and records the
// ARCHIVE deals that make a range archived. Its cursor is the last height it attested; a restart
// resumes after it. It does not hold the node's block retain height: the chain's own Commit
// keeps that below the last archived height (docs/CHAIN.md, "History archiver").
type Runner struct {
	chain       Chain
	archiver    string
	nodeID      string
	dir         string
	width       int64
	dealsOpened uint64
	uploader    Uploader
	sleep       func(context.Context, time.Duration) error
	// piecesUploaded and uploadFailures count this process's uploads to providers.
	piecesUploaded uint64
	uploadFailures uint64
}

// NewRunner writes bundles under dir and signs attestations as archiver, the
// hot key of nodeID, an x/nodes node with an ARCHIVER role bond. uploader sends the bundle to
// the providers x/storage assigns to its ARCHIVE deals.
func NewRunner(chain Chain, uploader Uploader, archiver, nodeID, dir string, width int64) (*Runner, error) {
	if chain == nil || uploader == nil || archiver == "" || nodeID == "" || dir == "" {
		return nil, errors.New("archiver needs a chain, an uploader, a signer, a node id, and a directory")
	}
	if width < 1 {
		return nil, errors.New("range width must be positive")
	}
	if err := os.MkdirAll(filepath.Join(dir, "bundles"), 0o750); err != nil {
		return nil, fmt.Errorf("create bundle directory: %w", err)
	}
	return &Runner{chain: chain, uploader: uploader, sleep: sleepContext, archiver: archiver, nodeID: nodeID, dir: dir, width: width}, nil
}

func (r *Runner) cursorPath() string { return filepath.Join(r.dir, "cursor") }

// BundlePath is where the bundle of start-end is kept.
func BundlePath(dir string, start, end int64) string {
	return filepath.Join(dir, "bundles", fmt.Sprintf("%d-%d.orbh", start, end))
}

// Step attests every whole range that is finalised, moves every attested range toward archived
// by opening and recording its ARCHIVE deals, and writes monitor.json. It returns how many ranges
// it attested. x/archive accepts a range only once the chain is past its last height.
func (r *Runner) Step(ctx context.Context) (int, error) {
	done, attestErr := r.attestRanges(ctx)
	open, dealErr := r.advanceDeals(ctx)
	return done, errors.Join(attestErr, dealErr, r.writeMonitor(ctx, open))
}

func (r *Runner) attestRanges(ctx context.Context) (int, error) {
	cursor, err := LoadCursor(r.cursorPath())
	if err != nil {
		return 0, err
	}
	if cursor%r.width != 0 {
		return 0, fmt.Errorf("cursor %d is not on a %d-block range boundary", cursor, r.width)
	}
	done := 0
	var conflicts []error
	for {
		latest, err := r.chain.LatestHeight(ctx)
		if err != nil {
			return done, errors.Join(append(conflicts, err)...)
		}
		start, end := cursor+1, cursor+r.width
		if end >= latest {
			return done, errors.Join(conflicts...)
		}
		err = r.archiveRange(ctx, start, end)
		if errors.Is(err, ErrRootConflict) {
			conflicts = append(conflicts, err)
		} else if err != nil {
			return done, errors.Join(append(conflicts, err)...)
		} else {
			done++
		}
		if err := SaveCursor(r.cursorPath(), end); err != nil {
			return done, errors.Join(append(conflicts, err)...)
		}
		cursor = end
	}
}

// ConflictPath is where a conflicting range's local and on-chain values are kept.
func ConflictPath(dir string, start, end int64) string {
	return filepath.Join(dir, "conflicts", fmt.Sprintf("%d-%d.json", start, end))
}

func (r *Runner) recordConflict(start, end int64, local Bundle, cid string, rec types.RangeRecord) error {
	body, err := json.Marshal(map[string]string{
		"local_root": hex.EncodeToString(local.MerkleRoot), "local_hash": hex.EncodeToString(local.ContentHash), "local_cid": cid,
		"chain_root": hex.EncodeToString(rec.MerkleRoot), "chain_hash": hex.EncodeToString(rec.BundleHash), "chain_cid": rec.BundleCid,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(r.dir, "conflicts"), 0o750); err != nil {
		return fmt.Errorf("create conflict directory: %w", err)
	}
	if err := writeAtomic(ConflictPath(r.dir, start, end), body, 0o640); err != nil {
		return err
	}
	return fmt.Errorf("%w: %d-%d (details in %s)", ErrRootConflict, start, end, ConflictPath(r.dir, start, end))
}

func (r *Runner) archiveRange(ctx context.Context, start, end int64) error {
	blocks := make([]Block, 0, end-start+1)
	hashes := make([][]byte, 0, end-start+1)
	for h := start; h <= end; h++ {
		b, err := r.chain.Block(ctx, h)
		if err != nil {
			return err
		}
		blocks = append(blocks, b)
		hashes = append(hashes, b.Hash)
	}
	body, err := Encode(blocks)
	if err != nil {
		return err
	}
	if _, err := Decode(body); err != nil {
		return fmt.Errorf("range %d-%d: %w", start, end, err)
	}
	bundle, err := Pack(start, hashes, body)
	if err != nil {
		return fmt.Errorf("range %d-%d: %w", start, end, err)
	}
	rec, found, err := r.chain.Range(ctx, start, end)
	if err != nil {
		return err
	}
	cid := CID(body)
	if err := writeAtomic(BundlePath(r.dir, start, end), body, 0o640); err != nil {
		return err
	}
	pc, err := piece.Commit(body)
	if err != nil {
		return fmt.Errorf("range %d-%d: commit the bundle: %w", start, end, err)
	}
	local := types.Piece{Root: pc.Root, RealLeafCount: pc.RealLeafCount, PaddedLeafCount: pc.PaddedLeafCount, PieceBytes: uint64(len(body))}
	if found && (!bytes.Equal(rec.MerkleRoot, bundle.MerkleRoot) ||
		!bytes.Equal(rec.BundleHash, bundle.ContentHash) || rec.BundleCid != cid || !rec.PieceOf().Equal(local)) {
		return r.recordConflict(start, end, bundle, cid, rec)
	}
	if !found || !slices.Contains(rec.Archivers, r.archiver) {
		err = r.chain.Submit(ctx, &types.MsgAttest{
			Archiver: r.archiver, NodeId: r.nodeID, StartHeight: start, EndHeight: end,
			BundleCid: cid, BundleHash: bundle.ContentHash, MerkleRoot: bundle.MerkleRoot,
			PieceRoot: local.Root, RealLeafCount: local.RealLeafCount, PaddedLeafCount: local.PaddedLeafCount, PieceBytes: local.PieceBytes,
		})
		if err != nil {
			return err
		}
	}
	return r.track(start, end)
}

func writeAtomic(path string, body []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, mode); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
