// Package snapshot carries the nullifier database through state sync. The IAVL snapshot holds the
// accumulator and the count, not the nullifiers, because the set lives outside IAVL. Without this
// extension a state-synced node would start with an empty set and accept double spends, so a
// snapshot that lacks it is refused and a restore that does not fold to the committed accumulator
// fails.
package snapshot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	snapshots "github.com/cosmos/cosmos-sdk/store/v2/snapshots/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

const (
	// Name identifies the extension in the snapshot stream.
	Name = types.ModuleName + "_nullifiers"
	// Format is the payload format: a run of records, each height (u64 BE) then the nullifier.
	Format uint32 = 1

	recordLen = 8 + bundle.NodeLen
	// recordsPerPayload bounds one payload to about 160 KiB.
	recordsPerPayload = 4096
)

// Committed returns the accumulator and count the chain state committed to at a height.
type Committed func(height uint64) (accumulator [bundle.NodeLen]byte, count uint64, err error)

// Extension is the state-sync extension for the nullifier store.
type Extension struct {
	store     *nullifier.Store
	committed Committed
}

var _ snapshots.ExtensionSnapshotter = (*Extension)(nil)

// New returns the extension. committed reads the module state of a height from the restored
// IAVL store, so a restore can prove what it imported.
func New(store *nullifier.Store, committed Committed) *Extension {
	return &Extension{store: store, committed: committed}
}

// SnapshotName implements ExtensionSnapshotter.
func (*Extension) SnapshotName() string { return Name }

// SnapshotFormat implements ExtensionSnapshotter.
func (*Extension) SnapshotFormat() uint32 { return Format }

// SupportedFormats implements ExtensionSnapshotter.
func (*Extension) SupportedFormats() []uint32 { return []uint32{Format} }

// SnapshotExtension writes the first `count` records, where count is the number the chain
// committed to at height. Records past it belong to a block that ran but did not commit.
func (e *Extension) SnapshotExtension(height uint64, write snapshots.ExtensionPayloadWriter) error {
	_, count, err := e.committed(height)
	if err != nil {
		return fmt.Errorf("read the nullifier count at height %d: %w", height, err)
	}
	var written uint64
	payload := make([]byte, 0, recordsPerPayload*recordLen)
	stop := errors.New("enough")
	err = e.store.Walk(func(nf [bundle.NodeLen]byte, h int64) error {
		if written == count {
			return stop
		}
		payload = binary.BigEndian.AppendUint64(payload, uint64(h))
		payload = append(payload, nf[:]...)
		written++
		if len(payload) == recordsPerPayload*recordLen {
			if err := write(payload); err != nil {
				return err
			}
			payload = payload[:0]
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return fmt.Errorf("walk the nullifier store: %w", err)
	}
	if written != count {
		return fmt.Errorf("%w: state commits to %d nullifiers at height %d, the store holds %d",
			types.ErrNullifierStore, count, height, written)
	}
	if len(payload) > 0 {
		return write(payload)
	}
	return nil
}

// RestoreExtension imports the records into an empty store and refuses the restore unless they
// fold to the accumulator, and number the count, that the restored state committed to.
func (e *Extension) RestoreExtension(height uint64, format uint32, next snapshots.ExtensionPayloadReader) error {
	if format != Format {
		return fmt.Errorf("%w: nullifier snapshot format %d", snapshots.ErrUnknownFormat, format)
	}
	if empty, err := e.store.Empty(); err != nil || !empty {
		return fmt.Errorf("%w: restore needs an empty nullifier store (%v)", types.ErrNullifierStore, err)
	}
	imp := importer{store: e.store, height: int64(height), blockHeight: -1}
	for {
		payload, err := next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read a nullifier payload: %w", err)
		}
		if err := imp.add(payload); err != nil {
			return err
		}
	}
	if err := imp.flush(); err != nil {
		return err
	}
	wantAcc, wantCount, err := e.committed(height)
	if err != nil {
		return fmt.Errorf("read the committed nullifier state at height %d: %w", height, err)
	}
	if imp.acc != wantAcc || imp.count != wantCount {
		return fmt.Errorf("%w: imported %d nullifiers folding to %x, the state at height %d commits to %d folding to %x",
			types.ErrNullifierStore, imp.count, imp.acc, height, wantCount, wantAcc)
	}
	return nil
}

// importer writes payload records into the store one block at a time, in the order they arrive,
// and folds them into the accumulator the restored state has to match.
type importer struct {
	store       *nullifier.Store
	height      int64 // the snapshot height: no record may be above it
	acc         [bundle.NodeLen]byte
	count       uint64
	block       [][bundle.NodeLen]byte
	blockHeight int64
}

func (i *importer) add(payload []byte) error {
	if len(payload)%recordLen != 0 {
		return fmt.Errorf("%w: payload of %d bytes is not whole records", types.ErrNullifierStore, len(payload))
	}
	for ; len(payload) > 0; payload = payload[recordLen:] {
		h := int64(binary.BigEndian.Uint64(payload))
		if h < i.blockHeight || h > i.height {
			return fmt.Errorf("%w: record height %d is out of order or above the snapshot height %d", types.ErrNullifierStore, h, i.height)
		}
		if h != i.blockHeight {
			if err := i.flush(); err != nil {
				return err
			}
			i.blockHeight = h
		}
		var nf [bundle.NodeLen]byte
		copy(nf[:], payload[8:recordLen])
		i.block = append(i.block, nf)
		i.acc = nullifier.Fold(i.acc, nf)
		i.count++
	}
	return nil
}

// flush commits the block collected so far.
func (i *importer) flush() error {
	if len(i.block) == 0 {
		return nil
	}
	err := i.store.Commit(i.blockHeight, i.block)
	i.block = i.block[:0]
	if err != nil {
		return fmt.Errorf("%w: %w", types.ErrNullifierStore, err)
	}
	return nil
}
