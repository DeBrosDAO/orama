package steps

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Confirm step for reviewing and confirming installation
type Confirm struct {
	VpsIP             string
	Domain            string
	Branch            string
	NoPull            bool
	IsFirstNode       bool
	PeerDomain        string
	JoinAddress       string
	Peers             []string
	ClusterSecret     string
	SwarmKeyHex       string
	IPFSPeerID        string
	SNIWarning        string
}

// View renders the confirmation step
func (c *Confirm) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Confirm Installation") + "\n\n")

	noPullStr := "Pull latest"
	if c.NoPull {
		noPullStr = "Skip git pull"
	}

	config := fmt.Sprintf(
		"  VPS IP:     %s\n"+
			"  Domain:     %s\n"+
			"  Branch:     %s\n"+
			"  Git Pull:   %s\n"+
			"  Node Type:  %s\n",
		c.VpsIP,
		c.Domain,
		c.Branch,
		noPullStr,
		map[bool]string{true: "First node (new cluster)", false: "Join existing cluster"}[c.IsFirstNode],
	)

	if !c.IsFirstNode {
		config += fmt.Sprintf("  Peer Node:  %s\n", c.PeerDomain)
		config += fmt.Sprintf("  Join Addr:  %s\n", c.JoinAddress)
		if len(c.Peers) > 0 {
			config += fmt.Sprintf("  Bootstrap:  %s...\n", c.Peers[0][:40])
		}
		if len(c.ClusterSecret) >= 8 {
			config += fmt.Sprintf("  Secret:     %s...\n", c.ClusterSecret[:8])
		}
		if len(c.SwarmKeyHex) >= 8 {
			config += fmt.Sprintf("  Swarm Key:  %s...\n", c.SwarmKeyHex[:8])
		}
		if c.IPFSPeerID != "" {
			config += fmt.Sprintf("  IPFS Peer:  %s...\n", c.IPFSPeerID[:16])
		}
	}

	s.WriteString(boxStyle.Render(config))

	// Show SNI DNS warning if present
	if c.SNIWarning != "" {
		s.WriteString("\n\n")
		warningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500"))
		s.WriteString(warningStyle.Render(c.SNIWarning))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press Enter to install • Esc to go back"))
	return s.String()
}
