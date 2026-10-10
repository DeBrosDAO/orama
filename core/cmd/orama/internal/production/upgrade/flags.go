package upgrade

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
)

// Flags represents upgrade command flags
type Flags struct {
	Force           bool
	RestartServices bool
	SkipChecks      bool
	Nameserver      *bool // Pointer so we can detect if explicitly set vs default

	// PublicIP is this node's public address, recorded as node.public_ip.
	// Empty keeps the recorded one, or detects it (resolvePublicIP).
	PublicIP string

	// ACMECA is the ACME directory this node's Caddy issues from, recorded as
	// node.yaml tls.acme_ca: an alias (letsencrypt, letsencrypt-staging) or an
	// https URL, resolved by Resolve. Empty keeps the recorded one.
	ACMECA string

	// Remote upgrade flags
	Env        string // Target environment for remote rolling upgrade
	NodeFilter string // Single node IP to upgrade (optional)

	// Yes executes the printed rollout plan. Without it the plan is printed
	// and nothing is restarted: the plan is what an operator is approving —
	// which node is the leader, what order the restarts happen in — and it was
	// previously neither computed nor shown.
	Yes bool

	// Delay is how long a node has to rejoin the cluster after its upgrade
	// before the rollout gives up, in seconds.
	//
	// It used to be an unconditional sleep between nodes, which is not a gate:
	// it cannot tell a node that rejoined in 20 seconds from one that never
	// came back, so the next voter was restarted either way. It is now the
	// budget on a real readiness check.
	Delay int

	// ReexecedAfterBinarySwap is set by the orchestrator when it re-execs
	// itself with the NEWLY-INSTALLED binary, post Phase 2b. The new
	// process detects this flag, skips the pre-binary phases (1, 2, 2b)
	// already done by the old binary, and runs Phase 3+ using its OWN
	// up-to-date compiled config-generation logic. Closes bugboard #15
	// chicken-and-egg: pre-fix, Phase 4 ran with the old binary's
	// compiled Phase4GenerateConfigs, so config changes only took effect
	// on the NEXT rollout.
	//
	// Hidden flag — set programmatically by orchestrator.go via os.Args,
	// not a documented user-facing option.
	ReexecedAfterBinarySwap bool
}

// Resolve validates what the operator passed and puts it in the form the
// orchestrator and a remote node take: --acme-ca becomes the URL it names.
func (f *Flags) Resolve() error {
	if f.ACMECA == "" {
		return nil
	}
	url, err := oramainstall.ResolveACMECA(f.ACMECA)
	if err != nil {
		return clierr.Usage("--acme-ca: %v", err)
	}
	f.ACMECA = url
	return nil
}
