package repair

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/chain/storagekey"
)

// Seed is one deal the delegate serves. The file is <home>/deals/<id>.json,
// mode 0600, written by the delegate's operator when a client hands over the
// repair seed.
type Seed struct {
	DealID     uint64 `json:"deal_id"`
	RepairSeed string `json:"repair_seed"`
}

// LoadSeeds reads every deal file in dir. A file other users can read, a
// file whose name does not match its deal id, or a short seed is an error:
// the seed can rebuild any replica of that deal.
func LoadSeeds(dir string) (map[uint64][]byte, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[uint64][]byte{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list repair seeds in %s: %w", dir, err)
	}
	out := map[uint64][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id, seed, err := loadSeed(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[id] = seed
	}
	return out, nil
}

func loadSeed(path string) (uint64, []byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return 0, nil, fmt.Errorf("repair seed %s is mode %o; it must be 0600", path, info.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, fmt.Errorf("read %s: %w", path, err)
	}
	var s Seed
	if err := json.Unmarshal(body, &s); err != nil {
		return 0, nil, fmt.Errorf("repair seed %s is not valid JSON: %w", path, err)
	}
	if s.DealID == 0 || filepath.Base(path) != fmt.Sprintf("%d.json", s.DealID) {
		return 0, nil, fmt.Errorf("repair seed %s does not match its deal id %d", path, s.DealID)
	}
	seed, err := hex.DecodeString(s.RepairSeed)
	if err != nil || len(seed) < storagekey.MinSeedLen {
		return 0, nil, fmt.Errorf("repair seed %s: %w", path, storagekey.ErrSeed)
	}
	return s.DealID, seed, nil
}
