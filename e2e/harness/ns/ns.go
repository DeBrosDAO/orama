// Package ns creates a namespace per test, waits until it really serves, and
// deletes it when the test ends, verifying the teardown.
//
// "Ready" is never taken from the status flag alone: provisioning has reported
// ready for clusters that served nothing (see the namespace provisioning
// defects on the board), so readiness also requires a real request through the
// namespace's own gateway at https://ns-<name>.<base domain>.
package ns

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Budgets.
const (
	// ReadyBudget is how long provisioning may take (three nodes, rqlite +
	// olric + gateway + DNS + certificate).
	ReadyBudget = 10 * time.Minute
	// TeardownBudget is how long deletion may take to be visible everywhere.
	TeardownBudget = 5 * time.Minute
	// PollInterval is how often readiness and teardown are re-checked.
	PollInterval = 5 * time.Second
	// maxNameLength is the gateway's limit for a namespace name.
	maxNameLength = 40
	// randomSuffixLength is how many base32 characters make a name unique.
	randomSuffixLength = 6
)

// Via says who creates the namespace.
type Via int

const (
	// ViaUser creates it over HTTP as a fresh wallet signed in to the lobby.
	// The wallet is the owner. This is the default and is safe in parallel.
	ViaUser Via = iota
	// ViaOperator creates it with `orama namespace create` as the run's
	// operator; the operator wallet is the owner.
	ViaOperator
)

// Options configure New.
type Options struct {
	// Name is the namespace name; empty picks a unique e2e-... name.
	Name string
	Via  Via
	// DeviceAlg binds the owner's namespace session to a device (ViaUser only).
	DeviceAlg string
}

// Namespace is a ready namespace owned by the test.
type Namespace struct {
	Name      string
	ClusterID string
	// URL is the namespace gateway, https://ns-<name>.<base>.
	URL string
	// Owner is the owning wallet signed in to this namespace (nil for ViaOperator).
	Owner *gw.User
	// Client is aimed at the namespace gateway.
	Client *gw.Client
	// CLI is signed in to this namespace in a HOME of its own (ViaOperator only).
	CLI *oramacli.Runner
	// removed is set by MarkRemoved: something other than the owner removed
	// the namespace, so the owner's teardown has nothing left to delete.
	removed bool
}

// New creates a namespace, waits for it to serve and registers its deletion.
// It first takes a fleet-wide live-namespace slot (Reserve; E2E_MAX_LIVE_NAMESPACES,
// default DefaultMaxLive), shared by every package of the run, so parallel
// packages cannot exhaust the nodes' port blocks; the slot is released after
// the namespace's teardown. A test that creates more than one namespace
// calls Hold first: New fails a test that already holds a slot and has no
// held one left, because it would wait for a slot while holding one.
func New(t testing.TB, f *fleet.Fleet, opts Options) *Namespace {
	t.Helper()
	name := opts.Name
	if name == "" {
		name = UniqueName(t.Name())
	}
	if err := ValidName(name); err != nil {
		t.Fatal(err)
	}
	fleetSlot(t, f)
	switch opts.Via {
	case ViaUser:
		return newViaUser(t, f, name, opts)
	case ViaOperator:
		return newViaOperator(t, f, name)
	default:
		t.Fatalf("unknown ns.Via %d", opts.Via)
		return nil
	}
}

// UniqueName is "e2e-" + a hash of the test name + random bytes: stable enough
// to find in logs, unique across parallel tests and re-runs.
func UniqueName(testName string) string {
	sum := sha256.Sum256([]byte(testName))
	return "e2e-" + hex.EncodeToString(sum[:4]) + "-" + strings.ToLower(rand.Text()[:randomSuffixLength])
}

// ValidName mirrors the gateway's rule, so a bad name fails before a request.
func ValidName(name string) error {
	if len(name) < 2 || len(name) > maxNameLength {
		return fmt.Errorf("namespace name %q must be 2-%d characters", name, maxNameLength)
	}
	for i, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r == '-' && i > 0 && i < len(name)-1)
		if !ok {
			return fmt.Errorf("namespace name %q: lowercase letters, digits and inner hyphens only", name)
		}
	}
	return nil
}

// creationMode reads `orama cluster settings show` ("namespace-creation: <mode>").
func creationMode(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "namespace-creation:"); ok {
			return strings.TrimSpace(v), nil
		}
	}
	return "", fmt.Errorf("`orama cluster settings show` printed no namespace-creation line: %q", out)
}
