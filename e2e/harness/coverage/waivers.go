package coverage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Waiver says why an item has no e2e test yet and what would make one possible.
type Waiver struct {
	ID string `yaml:"id" json:"id"`
	// Reason is why it cannot be tested today.
	Reason string `yaml:"reason" json:"reason"`
	// Trigger is the event that ends the waiver ("RootWallet signs orama-tx headless, task 2857").
	Trigger string `yaml:"trigger" json:"trigger"`
}

type waiverFile struct {
	Waivers []Waiver `yaml:"waivers"`
}

// LoadWaivers reads waivers.yaml strictly. Shape problems of single entries
// (a missing reason) are the gate's to report, alongside everything else.
func LoadWaivers(path string) ([]Waiver, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read waivers %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var f waiverFile
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to parse waivers %s: %w", path, err)
	}
	return f.Waivers, nil
}
