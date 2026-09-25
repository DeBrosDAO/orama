package install

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/install/installers"
)

// PhaseTorSetup gives the node its anonymity backend: it removes whatever the
// Anyone network left behind, installs Tor — or upgrades it to the Tor Project
// repository's current release — and writes the Orama torrc. The supervisor
// then runs Tor as orama-namespace-tor@index.
//
// It needs the network. Install runs it once; upgrade runs it before the
// node's services are stopped, so a failure aborts the upgrade with the node
// still serving.
//
// Failure is fatal: /v1/proxy/anon, /v1/proxy/tunnel and anon_fetch exist on
// every node, and a node that silently lacks Tor serves them as 503s.
func (ps *ProductionSetup) PhaseTorSetup() error {
	ps.logf("Phase 2d: Setting up the Tor client (anonymity proxy)...")
	tor := installers.NewTorInstaller(ps.arch, ps.logWriter)
	return ps.setUpTor(tor, tor.Install)
}

// PhaseTorEnsure is PhaseTorSetup for the post-swap half of an upgrade, which
// runs under the NEW binary with the node's services already stopped. It
// touches the network only when Tor is missing or its repository is not
// current — on the upgrade that replaces Anyone, whose pre-stop half ran the
// OLD binary and knew nothing of Tor.
func (ps *ProductionSetup) PhaseTorEnsure() error {
	ps.logf("Phase 2d: Ensuring the Tor client (anonymity proxy)...")
	tor := installers.NewTorInstaller(ps.arch, ps.logWriter)
	return ps.setUpTor(tor, tor.EnsureInstalled)
}

func (ps *ProductionSetup) setUpTor(tor *installers.TorInstaller, install func() error) error {
	if err := installers.NewLegacyAnyoneCleaner(ps.oramaDir, ps.logWriter).Remove(); err != nil {
		return fmt.Errorf("remove the legacy Anyone network: %w", err)
	}
	if err := install(); err != nil {
		return fmt.Errorf("install the Tor client: %w", err)
	}
	if err := tor.Configure(); err != nil {
		return fmt.Errorf("configure the Tor client: %w", err)
	}
	return nil
}
