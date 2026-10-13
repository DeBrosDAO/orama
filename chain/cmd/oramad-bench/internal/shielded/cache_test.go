package shielded

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validCache() *Cache {
	return &Cache{
		Version: CacheVersion, ChainID: "orama-localnet-1", GenesisHash: "AA", AccountSeed: "s",
		AnchorHeight: 100, Anchor: "ab", TreeSize: 10, AnchorWindow: 14_400,
		Entries: []Entry{
			{ID: "t0", Kind: EntryTransfer, Actions: 2, Account: Signerless},
			{ID: "t1", Kind: EntryTransfer, Actions: 4, Account: Signerless},
			{ID: "u0", Kind: EntryUnshield, Actions: 2, Account: 3},
			{ID: "s0", Kind: EntryShield, Actions: 2, Account: 1},
		},
	}
}

func goodState() ChainState {
	return ChainState{ChainID: "orama-localnet-1", GenesisHash: "AA", Height: 150, TreeSize: 12}
}

func TestCacheValidate_acceptsAFreshCacheOnItsChain(t *testing.T) {
	if err := validCache().Validate(goodState(), DefaultAnchorMargin); err != nil {
		t.Fatal(err)
	}
}

func TestCacheValidate_refusesAStaleCache(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ChainState)
		cache  func(*Cache)
		want   string
	}{
		{"another chain id", func(s *ChainState) { s.ChainID = "orama-localnet-2" }, nil, "built for chain"},
		{"the chain was reset", func(s *ChainState) { s.GenesisHash = "BB" }, nil, "reset"},
		{"the chain is behind the cache", func(s *ChainState) { s.Height = 99 }, nil, "valid from height"},
		{"the note tree shrank", func(s *ChainState) { s.TreeSize = 9 }, nil, "note tree held"},
		{"the anchor left the window", func(s *ChainState) { s.Height = 100 + 14_400 }, nil, "anchor is"},
		{"inside the margin of the window", func(s *ChainState) { s.Height = 100 + 14_400 - 599 }, nil, "anchor is"},
		{"an old layout", nil, func(c *Cache) { c.Version = 0 }, "layout version"},
		{"a window no larger than the margin", nil, func(c *Cache) { c.AnchorWindow = 600 }, "no room"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cache, state := validCache(), goodState()
			if c.mutate != nil {
				c.mutate(&state)
			}
			if c.cache != nil {
				c.cache(cache)
			}
			err := cache.Validate(state, DefaultAnchorMargin)
			if !errors.Is(err, ErrStale) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want a stale error mentioning %q", err, c.want)
			}
		})
	}
}

func TestCacheValidate_lastValidBlockStillPasses(t *testing.T) {
	state := goodState()
	state.Height = 100 + 14_400 - 600
	if err := validCache().Validate(state, DefaultAnchorMargin); err != nil {
		t.Fatalf("a cache exactly at the margin was refused: %v", err)
	}
}

func TestCacheValidate_aCacheOfShieldsAloneNeverExpires(t *testing.T) {
	c := &Cache{
		Version: CacheVersion, ChainID: "orama-localnet-1", GenesisHash: "AA", AnchorWindow: 14_400,
		Entries: []Entry{{ID: "s0", Kind: EntryShield, Actions: 2, Account: 0}},
	}
	state := goodState()
	state.Height = 10_000_000
	if err := c.Validate(state, DefaultAnchorMargin); err != nil {
		t.Fatalf("an unanchored cache was refused for its age: %v", err)
	}
	state.GenesisHash = "BB"
	if err := c.Validate(state, DefaultAnchorMargin); !errors.Is(err, ErrStale) {
		t.Fatalf("a reset chain must still refuse it: %v", err)
	}
}

func TestCacheValidate_exhaustedCacheIsRefused(t *testing.T) {
	c := validCache()
	for i := range c.Entries {
		c.Entries[i].Used = true
	}
	if err := c.Validate(goodState(), DefaultAnchorMargin); !errors.Is(err, ErrExhausted) {
		t.Fatalf("got %v", err)
	}
}

func TestCachePeekAndMarkUsed_aUsedEntryIsNeverOfferedAgain(t *testing.T) {
	c := validCache()
	got := c.Peek(EntryTransfer, 2, 5)
	if len(got) != 1 || got[0].ID != "t0" {
		t.Fatalf("peeked %+v", got)
	}
	if again := c.Peek(EntryTransfer, 2, 5); len(again) != 1 {
		t.Fatal("Peek used the entry")
	}
	if n := c.MarkUsed([]string{"t0", "nope"}); n != 1 {
		t.Fatalf("marked %d", n)
	}
	if again := c.Peek(EntryTransfer, 2, 5); len(again) != 0 {
		t.Fatalf("a used entry was offered again: %+v", again)
	}
	if n := c.MarkUsed([]string{"t0"}); n != 0 {
		t.Fatalf("an entry already used was counted again: %d", n)
	}
	if any := c.Peek(EntryTransfer, 0, 5); len(any) != 1 || any[0].ID != "t1" {
		t.Fatalf("action count 0 must match any: %+v", any)
	}
	if none := c.Peek(EntryShield, 2, 0); len(none) != 0 {
		t.Fatal("n = 0 peeked something")
	}
	if c.Unused(EntryTransfer, 0) != 1 || c.Unused(EntryUnshield, 0) != 1 {
		t.Fatal("Unused disagrees with MarkUsed")
	}
}

func TestCacheSaveLoad_roundTripKeepsUsedFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "cache.json")
	c := validCache()
	c.MarkUsed([]string{"u0"})
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unused(EntryUnshield, 0) != 0 || got.Unused(EntryShield, 0) != 1 || got.ChainID != c.ChainID {
		t.Fatalf("round trip lost state: %+v", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("Save left %d files behind, want only the cache", len(entries))
	}
}

func TestLoadCache_errors(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadCache(filepath.Join(dir, "nope.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCache(bad); err == nil {
		t.Fatal("garbage was read as a cache")
	}
}
