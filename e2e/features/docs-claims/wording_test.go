//go:build e2e_fleet

package docsclaims

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
)

// Code facts the prose is compared with.
const (
	operatorPolicy = "core/pkg/gateway/handlers/operator/policy.go"
	emissionSplit  = "chain/x/emission/types/split.go"
	// defaultCap is the per-wallet cap with no setting row
	// (docs/SECURITY.md "default 10 when that row is absent").
	defaultCap = 10
	// supplyInvariant is the one-line supply rule bugboard 2855 asks CHAIN.md
	// to state verbatim (plans/open-network/track-c-chain.md C3 "total ==
	// emitted − burned").
	supplyInvariant = "supply == emitted - burned"
)

// TestNamespaceCap_whitepaperMatchesSecurity: the per-wallet namespace cap
// is a default of 10 that an operator may set from 1 to 10000
// (docs/SECURITY.md, docs/CLI_REFERENCE.md#orama-cluster-settings-set,
// core/pkg/gateway/handlers/operator/policy.go MaxNamespacesPerWalletCeiling).
// The whitepaper must not present 10 as a fixed limit (bugboard 2855).
func TestNamespaceCap_whitepaperMatchesSecurity(t *testing.T) {
	t.Parallel()
	ceiling := codeConst(t, operatorPolicy, "MaxNamespacesPerWalletCeiling")
	for _, doc := range []string{securityDoc, cliRefDoc} {
		text := cliconf.ReadRepoFile(t, doc)
		if !strings.Contains(text, ceiling) || !strings.Contains(text, "default") {
			t.Errorf("%s does not state the cap's default %d and ceiling %s", doc, defaultCap, ceiling)
		}
	}
	for _, l := range grep(t, whitepaper, regexp.MustCompile(`(?i)wallet[^.]*up to \d+ namespaces|up to \d+ namespaces[^.]*wallet`)) {
		if !regexp.MustCompile(`(?i)default|operator|configur`).MatchString(l.Text) {
			t.Errorf("%s\n  presents the per-wallet cap as fixed; it is a default of %d an operator sets from 1 to %s", l, defaultCap, ceiling)
		}
	}
}

// TestEmission_whitepaperDescribesBuiltCode: the whitepaper describes the
// code, the plan describes the future. The validator share the whitepaper
// states must be the code's (ValidatorSharePercent), and it must not say the
// other shares are never minted while CHAIN.md documents
// MintDevelopmentSpend and cumulative_service_minted (bugboard 2855: WP vs
// PLAN emission split wording).
func TestEmission_whitepaperDescribesBuiltCode(t *testing.T) {
	t.Parallel()
	validator := codeConst(t, emissionSplit, "ValidatorSharePercent")
	wp := cliconf.ReadRepoFile(t, whitepaper)
	if !strings.Contains(wp, validator+"%") {
		t.Errorf("%s does not state the %s%% validator share the code mints (%s)", whitepaper, validator, emissionSplit)
	}
	chain := cliconf.ReadRepoFile(t, chainDoc)
	if strings.Contains(chain, "MintDevelopmentSpend") || strings.Contains(chain, "cumulative_service_minted") {
		for _, l := range grep(t, whitepaper, regexp.MustCompile(`(?i)no module pays them|are not minted`)) {
			t.Errorf("%s\n  says the non-validator shares are never minted; %s documents MintDevelopmentSpend and service mints", l, chainDoc)
		}
	}
	for _, l := range grep(t, whitepaper, regexp.MustCompile(`(?i)minted lazily|lazily minted`)) {
		t.Errorf("%s\n  uses the plan's \"minted lazily\" wording (%s) in the document of built code", l, openNetPlan)
	}
}

// TestJoinTLS_notDescribedAsTOFU: a joining node pins the certificate
// fingerprint the invite carries (--ca-fingerprint, core/cmd/orama/internal/
// production/install), so its TLS is not trust on first use; SECURITY.md
// must not call it TOFU (bugboard 2855: stale TOFU wording).
func TestJoinTLS_notDescribedAsTOFU(t *testing.T) {
	t.Parallel()
	for _, l := range grep(t, securityDoc, regexp.MustCompile(`(?i)join[^.]{0,60}(TLS|certificate)[^.]{0,60}(TOFU|trust on first use)`)) {
		t.Errorf("%s\n  calls the join's TLS trust on first use; the invite pins the certificate fingerprint", l)
	}
}

// TestSupplyInvariant_statedInChainDoc: the supply invariant is stated in
// CHAIN.md in the one line operators and auditors look for (bugboard 2855:
// "stated in no doc verbatim: state it in CHAIN.md and test it"; the live
// check is TestSupplyInvariant_holdsOnEveryNode).
func TestSupplyInvariant_statedInChainDoc(t *testing.T) {
	t.Parallel()
	norm := strings.NewReplacer("−", "-", "`", "")
	if !strings.Contains(norm.Replace(cliconf.ReadRepoFile(t, chainDoc)), supplyInvariant) {
		t.Errorf("%s does not state %q", chainDoc, supplyInvariant)
	}
}

// TestOpenNetworkPlan_isAPlanNotADoc: plans/open-network.md is a design plan
// and lives outside docs/, which describe running code (CLAUDE.md "Docs
// describe what the code does today"); the docs that cite it must cite it as
// a plan, and its status must not deny code CHAIN.md documents as built
// (bugboard 2855). plans/ is gitignored (.gitignore: scratch plans), so the
// plan exists only in the author's checkout and not in a worktree, a clone
// or a release checkout: its header is checked where it is present, and the
// citation rule, which needs only the tracked docs, always.
func TestOpenNetworkPlan_isAPlanNotADoc(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(filepath.Join(cliconf.RepoRoot(t), filepath.FromSlash(openNetPlan))); err == nil {
		checkPlanHeader(t)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed to stat %s: %v", openNetPlan, err)
	}
	for _, doc := range docs(t) {
		for _, l := range grep(t, doc, regexp.MustCompile(`docs/open-network`)) {
			t.Errorf("%s\n  cites the plan under docs/; it is %s", l, openNetPlan)
		}
	}
}

// checkPlanHeader asserts the plan says it is a plan and does not deny the
// code CHAIN.md documents.
func checkPlanHeader(t *testing.T) {
	t.Helper()
	plan := lines(t, openNetPlan)
	var status line
	for _, l := range plan[:min(len(plan), 10)] {
		if strings.Contains(l.Text, "**Status:**") {
			status = l
		}
	}
	if !strings.Contains(status.Text, "Planned") {
		t.Errorf("%s has no \"**Status:** Planned\" line in its header", openNetPlan)
	}
	if strings.Contains(status.Text, "Nothing in this plan has been built") && strings.Contains(cliconf.ReadRepoFile(t, chainDoc), "chain/` actually does today") {
		t.Errorf("%s\n  says nothing is built, while %s documents the chain code that implements parts of it", status, chainDoc)
	}
}
