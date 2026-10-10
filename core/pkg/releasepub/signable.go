package releasepub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/secure-systems-lab/go-securesystemslib/cjson"
	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// MaxPayloadBytes is the largest payload the RootWallet agent signs.
const MaxPayloadBytes = 256 << 10

// specVersionPrefix is the TUF specification line the agent accepts (1.0.x).
const specVersionPrefix = "1.0."

// CheckSignable refuses a payload the RootWallet agent would refuse, before a
// person is asked to approve anything. The agent re-encodes the payload in
// canonical JSON and compares bytes, so the payload must be that encoding; it
// must open with the reserved prefix, name a positive version, a real expiry
// and a 1.0.x specification, be no larger than MaxPayloadBytes, list at most
// MaxTargets targets, and carry no delegations.
func CheckSignable(payload []byte) error {
	if len(payload) > MaxPayloadBytes {
		return fmt.Errorf("the payload is %d bytes, over the %d the RootWallet agent signs", len(payload), MaxPayloadBytes)
	}
	if !bytes.HasPrefix(payload, []byte(rwagent.ReleasePayloadPrefix)) {
		return fmt.Errorf("the payload does not open with %s", rwagent.ReleasePayloadPrefix)
	}
	again, err := cjson.EncodeCanonical(json.RawMessage(payload))
	if err != nil {
		return fmt.Errorf("re-encode the payload as canonical JSON: %w", err)
	}
	if !bytes.Equal(again, payload) {
		return fmt.Errorf("the payload is not canonical JSON: the agent re-encodes it and compares bytes")
	}
	var fields struct {
		Type        string                     `json:"_type"`
		Version     int64                      `json:"version"`
		Expires     string                     `json:"expires"`
		SpecVersion string                     `json:"spec_version"`
		Targets     map[string]json.RawMessage `json:"targets"`
		Delegations json.RawMessage            `json:"delegations"`
	}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return fmt.Errorf("read the payload: %w", err)
	}
	return checkFields(fields.Type, fields.Version, fields.Expires, fields.SpecVersion, len(fields.Targets), len(fields.Delegations) > 0)
}

func checkFields(roleType string, version int64, expires, spec string, targets int, delegations bool) error {
	switch roleType {
	case metadata.ROOT, metadata.TARGETS, metadata.SNAPSHOT, metadata.TIMESTAMP:
	default:
		return fmt.Errorf("_type %q is not root, targets, snapshot or timestamp", roleType)
	}
	if version < 1 {
		return fmt.Errorf("version %d is not positive", version)
	}
	if _, err := time.Parse(time.RFC3339, expires); err != nil {
		return fmt.Errorf("expires %q is not a time: %w", expires, err)
	}
	if !strings.HasPrefix(spec, specVersionPrefix) {
		return fmt.Errorf("spec_version %q is not %sx", spec, specVersionPrefix)
	}
	if targets > MaxTargets {
		return fmt.Errorf("the targets file lists %d targets, over the %d the agent signs; lower the retention", targets, MaxTargets)
	}
	if delegations {
		return fmt.Errorf("the agent refuses delegations: a channel is a target path prefix")
	}
	return nil
}
