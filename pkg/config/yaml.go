package config

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// DecodeStrict decodes YAML from a reader and rejects any unknown fields.
// This ensures the YAML only contains recognized configuration keys.
func DecodeStrict(r io.Reader, out interface{}) error {
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	return nil
}
