package wizard

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

// startCreate turns the run into the creation of a network: the machines are its
// first validators, so the questions about the global layer and the relay are not
// asked.
func (m *Model) startCreate() (tea.Model, tea.Cmd) {
	if m.opts.Create == nil {
		m.opts.Create = &setup.CreateOptions{}
	}
	m.opts.Network, m.opts.ClusterOnly = "", false
	m.toggles[optGlobal], m.toggles[optRelay], m.toggles[optExit] = true, false, false
	m.opts.Exit, m.opts.TorNetwork = false, ""
	return m.goTo(stepCreateName)
}

// takeCreate takes the answer to one of the three questions about the new network
// (one, for a network that is announced).
func (m *Model) takeCreate(value string) (tea.Model, tea.Cmd) {
	switch m.step {
	case stepCreateName:
		m.opts.Create.Name = strings.ToLower(value)
		if m.opts.Create.Name == "" {
			return m.fail("give the network a name")
		}
		if m.announced[m.opts.Create.Name] {
			// The announcement supplies the chain id and the release root.
			return m.goTo(stepStorage)
		}
		return m.goTo(stepCreateChainID)
	case stepCreateChainID:
		if value == "" {
			return m.fail("give the chain id, for example orama-%s-stagenet-1", m.opts.Create.Name)
		}
		m.opts.Create.ChainID = value
		return m.goTo(stepCreateRoot)
	}
	if value == "" {
		return m.fail("give the path of the release-root.json")
	}
	m.opts.Create.ReleaseRoot = value
	return m.goTo(stepStorage)
}
