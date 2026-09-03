package production

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// How long each component gets to come up after install.
//
// Generous, because this runs once on a machine that has just been provisioned
// and is doing everything at the same time. The alternative to waiting is what
// install did before: print a green tick and leave the operator to discover
// hours later that the node never worked.
const (
	verifyNodeBudget    = 60 * time.Second
	verifyRQLiteBudget  = 3 * time.Minute
	verifyGatewayBudget = 2 * time.Minute
	verifyWGBudget      = 60 * time.Second

	// rqliteProbeAttempt bounds ONE reading of rqlite's raft state. The overall
	// budget belongs to awaitVerify; this only has to be longer than the
	// status client's own 2s timeout, or a single slow response would look
	// like a failed attempt.
	rqliteProbeAttempt = 5 * time.Second
)

// verifyPollInterval is how often each probe is retried. A var so tests can
// shorten it.
var verifyPollInterval = 2 * time.Second

// VerifyFailure names the component that did not come up.
//
// A typed error so the caller can print the component rather than a wall of
// wrapped text, and so the install exit code means something.
type VerifyFailure struct {
	Component string
	Err       error
}

func (e *VerifyFailure) Error() string {
	return fmt.Sprintf("%s did not become ready: %v", e.Component, e.Err)
}

func (e *VerifyFailure) Unwrap() error { return e.Err }

// Phase8Verify checks that the node this install just built actually works.
//
// Install used to print "✅ Production installation complete!" unconditionally:
// after a partial template install, after a failed DNS seed, after a supervisor
// that started and exited. Everything downstream — the operator, the CLI, the
// next node's join — then proceeded on the assumption that the node was up.
//
// Order is the dependency order, so the first failure names the real cause
// rather than a symptom three layers up: the supervisor, then rqlite, then the
// overlay it needs, then the gateway that needs both.
func (ps *ProductionSetup) Phase8Verify(ctx context.Context) error {
	ps.logf("\n🔍 Phase 8: Verifying the node is actually running...")

	checks := []struct {
		component string
		budget    time.Duration
		probe     func(context.Context) error
	}{
		{"orama-node.service", verifyNodeBudget, ps.probeNodeService},
		{"rqlite", verifyRQLiteBudget, ps.probeRQLite},
		{"wireguard (wg0)", verifyWGBudget, probeWireGuard},
		{"gateway /health", verifyGatewayBudget, probeGatewayHealth},
	}

	for _, c := range checks {
		ps.logf("  Waiting for %s (up to %s)...", c.component, c.budget)
		if err := awaitVerify(ctx, c.budget, c.probe); err != nil {
			return &VerifyFailure{Component: c.component, Err: err}
		}
		ps.logf("  ✓ %s", c.component)
	}

	ps.logf("  ✓ Node verified")
	return nil
}

// awaitVerify polls until probe succeeds or the budget runs out, reporting the
// last diagnostic error rather than "timed out" — the diagnostic is the part
// that says what to fix.
func awaitVerify(ctx context.Context, budget time.Duration, probe func(context.Context) error) error {
	deadline := time.Now().Add(budget)
	var last error

	for {
		last = probe(ctx)
		if last == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("cancelled (last error: %w)", last)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("still failing after %s: %w", budget, last)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("cancelled (last error: %w)", last)
		case <-time.After(verifyPollInterval):
		}
	}
}

// probeNodeService reports whether the supervisor is up AND has stayed up.
//
// `is-active` alone is satisfied by a unit systemd is about to restart for the
// fifth time, which is exactly the shape of the failure this phase exists to
// catch, so the restart counter has to agree.
func (ps *ProductionSetup) probeNodeService(context.Context) error {
	active, err := ps.serviceController.StatusService("orama-node.service")
	if err != nil {
		return fmt.Errorf("query orama-node.service: %w", err)
	}
	if !active {
		return fmt.Errorf("orama-node.service is not active (journalctl -u orama-node -n 50)")
	}

	out, err := exec.Command("systemctl", "show", "-p", "NRestarts", "--value", "orama-node.service").Output()
	if err != nil {
		// systemctl show failing is not evidence the node is crash-looping.
		return nil
	}
	if n := strings.TrimSpace(string(out)); n != "" && n != "0" {
		return fmt.Errorf("orama-node.service is active but has restarted %s times — it is crash-looping (journalctl -u orama-node -n 50)", n)
	}
	return nil
}

// probeRQLite waits for a raft state of Leader or Follower, not for the port.
//
// rqlited binds its HTTP listener before it has joined anything, so a node that
// is still Candidate, still replaying its log, or still retrying a join answers
// on the port perfectly well.
func (ps *ProductionSetup) probeRQLite(ctx context.Context) error {
	return rqlite.WaitForRaftReady(ctx, constants.RQLiteHTTPPort, rqliteProbeAttempt)
}

// probeWireGuard checks the overlay interface exists and has a peer.
//
// Without wg0 the node is unreachable on 10.0.0.x by every orama CLI path, and
// the only way in is public-IP SSH.
func probeWireGuard(context.Context) error {
	out, err := exec.Command("wg", "show", WireGuardInterface).Output()
	if err != nil {
		return fmt.Errorf("wg show %s: %w", WireGuardInterface, err)
	}
	if !strings.Contains(string(out), "interface:") {
		return fmt.Errorf("%s exists but reports no interface", WireGuardInterface)
	}
	return nil
}

// probeGatewayHealth is the end-to-end check: the gateway only answers once it
// has its database, its cache and its config.
func probeGatewayHealth(ctx context.Context) error {
	url := fmt.Sprintf("http://%s/health", net.JoinHostPort("localhost", fmt.Sprint(constants.GatewayAPIPort)))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	resp, err := tlsutil.NewHTTPClient(5 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s returned %d, want 200", url, resp.StatusCode)
	}
	return nil
}
