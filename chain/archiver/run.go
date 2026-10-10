package archiver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// DefaultRangeBlocks is the genesis default of x/archive's range_blocks. x/archive accepts only
// canonical ranges (start at k*range_blocks+1, range_blocks long), so every archiver of a chain
// must use the chain's own width: the archiver command reads it from the chain (QueryRangeBlocks).
const DefaultRangeBlocks = types.DefaultRangeBlocks

// ErrRootConflict is a range that x/archive holds a different tuple for than the blocks this node
// reads: it was won by another tuple, or this archiver's own earlier attestation differs from what
// it reads now. x/archive tallies attestations per tuple, so a conflicting tuple on chain does not
// stop this archiver: it re-verifies its own bundle, keeps its own tuple and attests it. Only a
// range that another tuple has already won cannot be attested. The runner then writes
// conflicts/<start>-<end>.json, reports the error once, and moves on.
var ErrRootConflict = errors.New("range is held on chain with a different root, bundle or piece")

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
// keeps that below the last archived height (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md, "History archiver").
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

func (r *Runner) recordConflict(start, end int64, local Tuple, chain Tuple) error {
	body, err := json.Marshal(map[string]string{
		"local_root": hex.EncodeToString(local.MerkleRoot), "local_hash": hex.EncodeToString(local.BundleHash), "local_cid": local.BundleCid,
		"chain_root": hex.EncodeToString(chain.MerkleRoot), "chain_hash": hex.EncodeToString(chain.BundleHash), "chain_cid": chain.BundleCid,
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

// packRange reads the blocks of a range from the node and packs them into a bundle.
func (r *Runner) packRange(ctx context.Context, start, end int64) (Bundle, []byte, error) {
	blocks := make([]Block, 0, end-start+1)
	hashes := make([][]byte, 0, end-start+1)
	for h := start; h <= end; h++ {
		b, err := r.chain.Block(ctx, h)
		if err != nil {
			return Bundle{}, nil, err
		}
		blocks = append(blocks, b)
		hashes = append(hashes, b.Hash)
	}
	body, err := Encode(blocks)
	if err != nil {
		return Bundle{}, nil, err
	}
	if _, err := Decode(body); err != nil {
		return Bundle{}, nil, fmt.Errorf("range %d-%d: %w", start, end, err)
	}
	bundle, err := Pack(start, hashes, body)
	if err != nil {
		return Bundle{}, nil, fmt.Errorf("range %d-%d: %w", start, end, err)
	}
	return bundle, body, nil
}

// tupleOf is what an attestation of body and its bundle states: the tuple x/archive tallies.
func tupleOf(bundle Bundle, body []byte) (Tuple, error) {
	pc, err := piece.Commit(body)
	if err != nil {
		return Tuple{}, fmt.Errorf("range %d-%d: commit the bundle: %w", bundle.Start, bundle.End, err)
	}
	return Tuple{
		BundleCid: CID(body), BundleHash: bundle.ContentHash, MerkleRoot: bundle.MerkleRoot,
		Piece: types.Piece{Root: pc.Root, RealLeafCount: pc.RealLeafCount, PaddedLeafCount: pc.PaddedLeafCount, PieceBytes: uint64(len(body))},
	}, nil
}

// Tuple is the tuple x/archive tallies attestations by.
type Tuple = types.Tuple

// reverify reads the range's blocks from the node a second time and checks that they pack into the
// same bundle. An archiver that finds itself in disagreement with what is on chain must know that its
// own tuple is what this node's blocks say, not a bad read, before it keeps it against the others.
func (r *Runner) reverify(ctx context.Context, start, end int64, local Tuple) error {
	bundle, body, err := r.packRange(ctx, start, end)
	if err != nil {
		return fmt.Errorf("range %d-%d disagrees with the chain and could not be read again: %w", start, end, err)
	}
	again, err := tupleOf(bundle, body)
	if err != nil {
		return err
	}
	if !again.Equal(local) {
		return fmt.Errorf("range %d-%d disagrees with the chain and this node's blocks changed between two reads: not attesting", start, end)
	}
	return nil
}

func (r *Runner) archiveRange(ctx context.Context, start, end int64) error {
	bundle, body, err := r.packRange(ctx, start, end)
	if err != nil {
		return err
	}
	local, err := tupleOf(bundle, body)
	if err != nil {
		return err
	}
	rec, found, err := r.chain.Range(ctx, start, end)
	if err != nil {
		return err
	}
	if err := writeAtomic(BundlePath(r.dir, start, end), body, 0o640); err != nil {
		return err
	}
	attested := false
	if found {
		if mine, ok := rec.AttestedBy(r.archiver); ok {
			if !mine.Equal(local) {
				return r.recordConflict(start, end, local, mine)
			}
			attested = true
		} else if rec.Contested(local) {
			// Another tuple is on chain. Attestations are tallied per tuple, so this archiver
			// keeps its own once it has checked it, unless that tuple has already won the range.
			if err := r.reverify(ctx, start, end, local); err != nil {
				return err
			}
			if rec.Decided {
				return r.recordConflict(start, end, local, rec.Winner())
			}
		}
	}
	if !attested {
		err = r.chain.Submit(ctx, &types.MsgAttest{
			Archiver: r.archiver, NodeId: r.nodeID, StartHeight: start, EndHeight: end,
			BundleCid: local.BundleCid, BundleHash: local.BundleHash, MerkleRoot: local.MerkleRoot,
			PieceRoot: local.Piece.Root, RealLeafCount: local.Piece.RealLeafCount, PaddedLeafCount: local.Piece.PaddedLeafCount, PieceBytes: local.Piece.PieceBytes,
		})
		if isConflictingAttestation(err) {
			// Another key of this operator already attested a different tuple for the range, and
			// x/archive counts an operator toward one tuple per range: this archiver can never
			// attest its own. Record the two once and move on instead of retrying every pass.
			return r.recordConflict(start, end, local, otherTuple(rec, local))
		}
		if err != nil {
			return err
		}
	}
	return r.track(start, end)
}

// isConflictingAttestation reports whether err is the chain refusing an attestation because the
// operator already attested another tuple of the range. The chain reports it as text in the
// transaction log, so the sentinel's message is what identifies it.
func isConflictingAttestation(err error) bool {
	return err != nil && strings.Contains(err.Error(), types.ErrConflictingAttestation.Error())
}

// otherTuple is the first tuple of rec that is not local, or an empty tuple when rec holds none.
func otherTuple(rec types.RangeRecord, local Tuple) Tuple {
	if rec.Decided && !rec.Winner().Equal(local) {
		return rec.Winner()
	}
	for _, c := range rec.Candidates {
		if !c.Tuple().Equal(local) {
			return c.Tuple()
		}
	}
	return Tuple{}
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
