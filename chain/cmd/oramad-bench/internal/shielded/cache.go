package shielded

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CacheVersion changes whenever the cache layout or what a valid entry means does, so an old file
// is refused rather than half-read.
const CacheVersion = 1

// DefaultAnchorMargin is how many blocks before its anchor leaves the chain's window a cache is
// already refused, so a run that starts on the last valid block does not end on an invalid one.
const DefaultAnchorMargin uint64 = 600

// Entry kinds.
const (
	EntryShield   = "shield"
	EntryTransfer = "transfer"
	EntryUnshield = "unshield"
)

// Signerless is the Account of an entry that needs no signature (a shielded transfer).
const Signerless = -1

// ErrStale is returned for a cache the chain it is checked against has moved past or never was.
var ErrStale = errors.New("shielded cache is stale")

// ErrExhausted is returned when every entry of a cache has been used: a nullifier is spendable
// once, so a used entry is never sent again.
var ErrExhausted = errors.New("shielded cache has no unused bundle")

// Entry is one pre-built transaction body.
type Entry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Actions int    `json:"actions"`
	// Account is the index of the bench account that signs the transaction, or Signerless.
	Account      int    `json:"account"`
	Bundle       string `json:"bundle"`
	ValueBalance int64  `json:"value_balance"`
	// Used is set once the entry was handed to a run: whether the chain included it or not, its
	// nullifiers may be spent now, so it is not offered again.
	Used bool `json:"used"`
}

// Cache is the set of bundles built for one chain at one height.
type Cache struct {
	Version int    `json:"version"`
	ChainID string `json:"chain_id"`
	// GenesisHash is the hash of block 1: a chain that was reset keeps its chain id and loses it.
	GenesisHash string `json:"genesis_hash"`
	AccountSeed string `json:"account_seed"`
	// AnchorHeight is the height of the last block that changed the note tree before the spends
	// were anchored to its root; the anchor is valid for AnchorWindow blocks after it.
	AnchorHeight int64  `json:"anchor_height"`
	Anchor       string `json:"anchor"`
	// TreeSize is the number of note commitments the tree held at AnchorHeight.
	TreeSize     uint64    `json:"tree_size"`
	AnchorWindow uint64    `json:"anchor_window_blocks"`
	CreatedAt    time.Time `json:"created_at"`
	Entries      []Entry   `json:"entries"`
}

// ChainState is what the chain says now, to check a cache against.
type ChainState struct {
	ChainID     string
	GenesisHash string
	Height      int64
	TreeSize    uint64
}

// Validate is nil when the cache can be used against state. margin is how many blocks of the
// anchor window must remain.
func (c *Cache) Validate(state ChainState, margin uint64) error {
	switch {
	case c.Version != CacheVersion:
		return fmt.Errorf("%w: layout version %d, this tool reads %d", ErrStale, c.Version, CacheVersion)
	case c.ChainID != state.ChainID:
		return fmt.Errorf("%w: built for chain %q, the chain is %q", ErrStale, c.ChainID, state.ChainID)
	case c.GenesisHash != state.GenesisHash:
		return fmt.Errorf("%w: built for genesis %s, the chain's is %s (it was reset)", ErrStale, c.GenesisHash, state.GenesisHash)
	}
	// A cache of shields alone has no anchor: its bundles spend nothing real and never go stale.
	if c.Anchor != "" {
		if err := c.validateAnchor(state, margin); err != nil {
			return err
		}
	}
	if c.Unused(EntryShield, 0)+c.Unused(EntryTransfer, 0)+c.Unused(EntryUnshield, 0) == 0 {
		return ErrExhausted
	}
	return nil
}

func (c *Cache) validateAnchor(state ChainState, margin uint64) error {
	switch {
	case state.Height < c.AnchorHeight:
		return fmt.Errorf("%w: valid from height %d, the chain is at %d", ErrStale, c.AnchorHeight, state.Height)
	case state.TreeSize < c.TreeSize:
		return fmt.Errorf("%w: the note tree held %d notes when it was built and holds %d now", ErrStale, c.TreeSize, state.TreeSize)
	case c.AnchorWindow <= margin:
		return fmt.Errorf("%w: the anchor window of %d blocks leaves no room for a margin of %d", ErrStale, c.AnchorWindow, margin)
	}
	if age := uint64(state.Height - c.AnchorHeight); age > c.AnchorWindow-margin {
		return fmt.Errorf("%w: the anchor is %d blocks old, the window is %d (margin %d)", ErrStale, age, c.AnchorWindow, margin)
	}
	return nil
}

// Unused counts the unused entries of a kind; actions 0 matches any action count.
func (c *Cache) Unused(kind string, actions int) int {
	n := 0
	for _, e := range c.Entries {
		if e.Kind == kind && !e.Used && (actions == 0 || e.Actions == actions) {
			n++
		}
	}
	return n
}

// Peek returns up to n unused entries of a kind and action count (0: any), in cache order,
// without using them.
func (c *Cache) Peek(kind string, actions, n int) []Entry {
	var out []Entry
	for _, e := range c.Entries {
		if len(out) == n {
			break
		}
		if e.Kind == kind && !e.Used && (actions == 0 || e.Actions == actions) {
			out = append(out, e)
		}
	}
	return out
}

// MarkUsed marks the entries with the given ids used and returns how many it found. The cache is
// not saved; the caller saves it once the run is over.
func (c *Cache) MarkUsed(ids []string) int {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	n := 0
	for i := range c.Entries {
		if want[c.Entries[i].ID] && !c.Entries[i].Used {
			c.Entries[i].Used = true
			n++
		}
	}
	return n
}

// Save writes the cache atomically, so an interrupted run cannot leave half a file that a later
// run would read as a smaller cache.
func (c *Cache) Save(path string) error {
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the shielded cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create the cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".shielded-cache-*")
	if err != nil {
		return fmt.Errorf("create a temporary cache file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write the shielded cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close the shielded cache: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("move the shielded cache into place: %w", err)
	}
	return nil
}

// LoadCache reads a cache file. A missing file is os.ErrNotExist.
func LoadCache(path string) (*Cache, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Cache
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("decode the shielded cache %s: %w", path, err)
	}
	return &c, nil
}
