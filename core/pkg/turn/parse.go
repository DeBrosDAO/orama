package turn

import (
	"bytes"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/config"
)

// ParseConfig strictly decodes the TURN YAML written by the namespace spawner
// (a yaml.Marshal of Config) back into a Config.
//
// Strict decoding rejects unknown keys, so the writer and the reader must agree
// on every field. This used to be enforced by a hand-maintained mirror struct in
// cmd/turn, which meant any new field on Config crashed the TURN binary at
// startup until someone remembered to add it there too — bugboard #283 hit
// exactly that with `tenants`. Decoding into Config itself makes the struct's
// own yaml tags the single definition of the contract, so the two cannot drift.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := config.DecodeStrict(bytes.NewReader(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse TURN config: %w", err)
	}
	return &cfg, nil
}
