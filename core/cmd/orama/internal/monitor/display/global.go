package display

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// validatorStatus is the node-summary words for a chain node that is in the
// validator set: its power, and jailed, tombstoned or missed blocks when the
// signing query answered. An empty string is a node that is not a validator.
func validatorStatus(t view.Theme, c *report.ChainReport) string {
	if !c.IsValidator {
		return ""
	}
	parts := []string{fmt.Sprintf("validator (power %d)", c.VotingPower)}
	switch {
	case c.Tombstoned != nil && *c.Tombstoned:
		parts = append(parts, t.Crit.Render("TOMBSTONED"))
	case c.Jailed != nil && *c.Jailed:
		parts = append(parts, t.Crit.Render("JAILED"))
	}
	if c.MissedBlockRatio != nil {
		parts = append(parts, fmt.Sprintf("missed %.2f%%", *c.MissedBlockRatio*100))
	}
	if c.SigningError != "" {
		parts = append(parts, t.Warn.Render("signing status unknown"))
	}
	return strings.Join(parts, ", ")
}

// globalLine summarises the global services on a node: the public Kubo's disk
// use, the storage provider's deal slots, proof misses and hot-key balance, and
// whether the relay or directory authority is in the relay set. Empty when no global unit is installed.
func globalLine(t view.Theme, g *report.GlobalReport) string {
	if g == nil {
		return ""
	}
	if g.Error != "" {
		return t.Crit.Render(g.Error)
	}
	var parts []string
	if state, ok := unitState(g, constants.GlobalIPFSUnit); ok {
		parts = append(parts, "public Kubo "+unitLabel(t, state)+kuboUse(g.PublicIPFS))
	}
	if state, ok := unitState(g, constants.GlobalProviderUnit); ok {
		parts = append(parts, "provider "+unitLabel(t, state)+providerFacts(g.Provider))
	}
	if state, ok := unitState(g, constants.GlobalTorRelayUnit); ok {
		parts = append(parts, "relay "+unitLabel(t, state)+relayFacts(t, g.Relay))
	}
	if state, ok := unitState(g, constants.GlobalTorDirauthUnit); ok {
		parts = append(parts, "directory authority "+unitLabel(t, state)+relayFacts(t, g.Relay))
	}
	return strings.Join(parts, " | ")
}

func unitState(g *report.GlobalReport, name string) (string, bool) {
	for _, u := range g.Units {
		if u.Name == name {
			return u.State, true
		}
	}
	return "", false
}

func unitLabel(t view.Theme, state string) string {
	if state == "active" {
		return t.OK.Render(state)
	}
	return t.Crit.Render(state)
}

func kuboUse(p *report.PublicIPFSReport) string {
	if p == nil || p.Error != "" || p.StorageMaxBytes == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d of %d MB)", p.RepoBytes>>20, p.StorageMaxBytes>>20)
}

func providerFacts(p *report.ProviderReport) string {
	if p == nil || p.Error != "" {
		return ""
	}
	var facts []string
	if p.HeldSlots != nil {
		facts = append(facts, fmt.Sprintf("%d slots held", *p.HeldSlots))
	}
	if p.PendingSlots != nil {
		facts = append(facts, fmt.Sprintf("%d pending", *p.PendingSlots))
	}
	if p.ProofMisses != nil {
		facts = append(facts, fmt.Sprintf("%d proof misses", *p.ProofMisses))
	}
	if p.HotKeyBalanceNorama != nil {
		facts = append(facts, fmt.Sprintf("hot key %d norama", *p.HotKeyBalanceNorama))
	}
	if len(facts) == 0 {
		return ""
	}
	return " (" + strings.Join(facts, ", ") + ")"
}

func relayFacts(t view.Theme, r *report.RelayReport) string {
	if r == nil || r.Error != "" || r.InConsensus == nil {
		return ""
	}
	if *r.InConsensus {
		return " (in the relay set)"
	}
	return " (" + t.Warn.Render("not in the relay set") + ")"
}
