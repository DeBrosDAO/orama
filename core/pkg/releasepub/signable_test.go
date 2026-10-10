package releasepub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const goodTimestamp = `{"_type":"timestamp","expires":"2026-10-17T12:00:00Z","meta":{"snapshot.json":{"version":1}},"spec_version":"1.0.31","version":1}`

func TestCheckSignable_acceptsCanonicalPayloads(t *testing.T) {
	if err := CheckSignable([]byte(goodTimestamp)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSignable_refusals(t *testing.T) {
	targets := func(n int) string {
		entries := make([]string, n)
		for i := range entries {
			entries[i] = fmt.Sprintf(`"nightly/orama-0.3.%03d-linux-amd64.tar.gz":{"hashes":{"sha256":"ab"},"length":1}`, i)
		}
		return `{"_type":"targets","expires":"2026-10-17T12:00:00Z","spec_version":"1.0.31","targets":{` + strings.Join(entries, ",") + `},"version":1}`
	}
	cases := map[string]string{
		"whitespace (not canonical)":   strings.Replace(goodTimestamp, `"meta":`, `"meta": `, 1),
		"keys out of order":            `{"_type":"timestamp","version":1,"expires":"2026-10-17T12:00:00Z","meta":{},"spec_version":"1.0.31"}`,
		"another type":                 strings.Replace(goodTimestamp, `"timestamp"`, `"mirrors"`, 1),
		"version zero":                 strings.Replace(goodTimestamp, `"spec_version":"1.0.31","version":1}`, `"spec_version":"1.0.31","version":0}`, 1),
		"no real expiry":               strings.Replace(goodTimestamp, `2026-10-17T12:00:00Z`, `soon`, 1),
		"a spec version from 2.x":      strings.Replace(goodTimestamp, `1.0.31`, `2.0.0`, 1),
		"not opening with the prefix":  `{"expires":"2026-10-17T12:00:00Z"}`,
		"201 targets":                  targets(MaxTargets + 1),
		"delegations":                  `{"_type":"targets","delegations":{"keys":{},"roles":[]},"expires":"2026-10-17T12:00:00Z","spec_version":"1.0.31","targets":{},"version":1}`,
		"not JSON":                     `{"_type":"timestamp"`,
		"over 256 KiB":                 `{"_type":"timestamp","pad":"` + strings.Repeat("a", MaxPayloadBytes) + `"}`,
		"floating point (not allowed)": strings.Replace(goodTimestamp, `"spec_version":"1.0.31","version":1}`, `"spec_version":"1.0.31","version":1.5}`, 1),
	}
	for name, payload := range cases {
		if err := CheckSignable([]byte(payload)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := CheckSignable([]byte(targets(MaxTargets))); err != nil {
		t.Errorf("exactly %d targets: %v", MaxTargets, err)
	}
}

// What the agent re-encodes is the payload itself: for every role of a real
// release, re-encoding gives the same bytes.
func TestCheckSignable_everyRoleOfARealReleaseIsAFixedPoint(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	if _, err := Cut(t.Context(), cutParams(repo, agent, mustChannel(t, "nightly"), archive(t, "0.3.1", "amd64", "x"))); err != nil {
		t.Fatal(err)
	}
	if agent.approvals() != 4 {
		t.Fatalf("%d payloads, want root + targets + snapshot + timestamp", agent.approvals())
	}
	for _, payload := range agent.payloads {
		if !bytes.Equal(canonicalAgain(t, payload), payload) {
			t.Errorf("not a fixed point of the canonical encoding: %s", payload)
		}
		var generic map[string]any
		if err := json.Unmarshal(payload, &generic); err != nil {
			t.Fatal(err)
		}
		if _, hasDelegations := generic["delegations"]; hasDelegations {
			t.Errorf("a payload carries delegations: %s", payload)
		}
	}
}
