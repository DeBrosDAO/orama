package upgrade

import (
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/lifecycle"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/utils"
	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
)

// newOramaBinaryPath is the on-disk path Phase 2b installs the new
// orama binary to. Re-exec target for bugboard #15 chicken-and-egg fix.
const newOramaBinaryPath = "/opt/orama/bin/orama"

// Orchestrator manages the upgrade process.
type Orchestrator struct {
	oramaHome string
	oramaDir  string
	setup     *oramainstall.ProductionSetup
	flags     *Flags
	// ops is every side effect Execute performs. NewOrchestrator wires the
	// real ones (realOps); a test replaces them to check the order.
	ops upgradeOps
	// units is systemd as stopServices sees it; nil is the node's.
	units unitController
}

// upgradeOps are the steps of an upgrade, as functions, so the order Execute
// runs them in can be tested without a node.
type upgradeOps struct {
	isUpdate func() bool

	// Before anything is stopped, while the node still serves.
	preferences      func() error
	prerequisites    func() error
	provision        func() error
	torSetup         func() error
	verifyArchive    func() error
	resolvePublicIP  func() error
	recordRaftID     func() error
	handOverAndFence func() error

	// The stop and the binary swap.
	stopServices    func() error
	portsFree       func() error
	installBinaries func() error
	reexec          func() error

	// Under the new binary.
	secrets        func() error
	configs        func() error
	initServices   func() error
	torEnsure      func() error
	privHelper     func() error
	removeKeys     func() error
	templates      func() error
	systemdUnits   func() error
	firewall       func() error
	restart        func() error
	retireLegacy   func() error
	clearMaintMode func() error
}

// NewOrchestrator creates a new upgrade orchestrator
func NewOrchestrator(flags *Flags) *Orchestrator {
	oramaHome := oramainstall.OramaBase
	oramaDir := oramainstall.OramaDir

	// Load existing preferences
	prefs := oramainstall.LoadPreferences(oramaDir)

	// Use saved nameserver preference if not explicitly specified
	isNameserver := prefs.Nameserver
	if flags.Nameserver != nil {
		isNameserver = *flags.Nameserver
	}

	setup := oramainstall.NewProductionSetup(oramaHome, os.Stdout, flags.Force, flags.SkipChecks)
	setup.SetNameserver(isNameserver)

	o := &Orchestrator{
		oramaHome: oramaHome,
		oramaDir:  oramaDir,
		setup:     setup,
		flags:     flags,
	}
	o.ops = o.realOps()
	return o
}

// realOps wires every step to the node.
func (o *Orchestrator) realOps() upgradeOps {
	s := o.setup
	return upgradeOps{
		isUpdate:         s.IsUpdate,
		preferences:      o.handleBranchPreferences,
		prerequisites:    s.Phase1CheckPrerequisites,
		provision:        s.Phase2ProvisionEnvironment,
		torSetup:         s.PhaseTorSetup,
		verifyArchive:    s.VerifyPreBuiltArchive,
		resolvePublicIP:  o.resolveAndSetPublicIP,
		recordRaftID:     o.recordRaftIdentity,
		handOverAndFence: lifecycle.HandlePreUpgrade,
		stopServices:     o.stopServices,
		portsFree:        func() error { return utils.EnsurePortsAvailable("prod upgrade", utils.DefaultPorts()) },
		installBinaries:  s.Phase2bInstallBinaries,
		reexec:           o.reexecAfterBinarySwap,
		secrets:          s.Phase3GenerateSecrets,
		configs:          o.regenerateConfigs,
		initServices:     o.initializeServices,
		torEnsure:        s.PhaseTorEnsure,
		privHelper:       s.EnsurePrivHelper,
		removeKeys:       s.RemoveCopiedSigningKeys,
		templates:        s.InstallNamespaceTemplates,
		systemdUnits: func() error {
			enableHTTPS, _, _ := o.extractGatewayConfig()
			return s.Phase5CreateSystemdServices(enableHTTPS)
		},
		firewall:       func() error { return s.Phase6bSetupFirewall(false) },
		restart:        o.restartServices,
		retireLegacy:   retireLegacyDeploymentUnits,
		clearMaintMode: lifecycle.ClearMaintenanceFlag,
	}
}

// upgradeStep is one step of an upgrade.
type upgradeStep struct {
	name   string
	banner string
	run    func() error
}

// runSteps runs steps in order and stops at the first failure, naming it and
// saying what state the node was left in.
func runSteps(steps []upgradeStep, state string) error {
	for _, step := range steps {
		fmt.Print(step.banner)
		if err := step.run(); err != nil {
			if state != "" {
				return fmt.Errorf("%s (%s): %w", step.name, state, err)
			}
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return nil
}

// preStopSteps run while the node still serves, before anything is stopped;
// a failure aborts the upgrade with the node untouched.
//
//   - Tor: it needs the network (it upgrades Tor from deb.torproject.org).
//   - The build archive: Phase 2b refuses an archive that does not verify
//     against the trust anchor, or whose signer rotation would be refused, and
//     it runs after the services are stopped. Checking here first means such
//     an archive never takes the node down.
//   - node.public_ip: Phase 4 records it and the upgrade fails without one; a
//     node behind NAT has to be told with --public-ip, and learning that after
//     the stop left the node down until the upgrade was re-run.
//
// On an existing install, three more, in this order:
//
//   - The raft identity: the id the index rqlited runs under, the address its
//     configuration holds it at and the cluster's members, read from the
//     running rqlited and recorded beside its raft state. The release that
//     moves the index raft port changes the address the node listens on; with
//     the id and the old address recorded, the node restarts under the same
//     id and joins again so the leader re-registers it (pkg/namespace
//     indexJoinTargets). Nothing else on the node knows the id once node.yaml
//     has been regenerated.
//   - Hand-over: the quorum check, the maintenance flag, leadership transfer
//     on the index (and each namespace) rqlite, and confirmation that another
//     voter leads (lifecycle.HandlePreUpgrade). It has to run here, against
//     the running node: the stop below is what removes this voter, so the
//     leader must have stepped down before it, and after the stop there is no
//     rqlited left to read.
func (o *Orchestrator) preStopSteps() []upgradeStep {
	steps := []upgradeStep{
		{name: "saving the preferences failed", run: o.ops.preferences},
		{name: "prerequisites check failed", banner: "\n📋 Phase 1: Checking prerequisites...\n", run: o.ops.prerequisites},
		{name: "environment provisioning failed", banner: "\n🛠️  Phase 2: Provisioning environment...\n", run: o.ops.provision},
		{name: "tor setup failed", banner: "\nPhase 2d: Installing/upgrading the Tor client...\n", run: o.ops.torSetup},
		{name: "the build archive does not verify", banner: "\n🔏 Verifying the build archive before stopping anything...\n", run: o.ops.verifyArchive},
		{name: "node.public_ip cannot be determined", banner: "\n🌐 Resolving node.public_ip...\n", run: o.ops.resolvePublicIP},
	}
	if !o.ops.isUpdate() {
		return steps
	}
	return append(steps,
		upgradeStep{name: "recording the raft identity failed", banner: "\n🪪 Recording the index rqlite's raft identity...\n", run: o.ops.recordRaftID},
		upgradeStep{name: "this node is not safe to stop", banner: "\n🤝 Handing over before the stop...\n", run: o.ops.handOverAndFence},
	)
}

// swapSteps stop the node and install the new binaries.
func (o *Orchestrator) swapSteps() []upgradeStep {
	var steps []upgradeStep
	if o.ops.isUpdate() {
		steps = append(steps, upgradeStep{name: "stopping the services failed", run: o.ops.stopServices})
	}
	return append(steps,
		upgradeStep{name: "ports are still in use", run: o.ops.portsFree},
		upgradeStep{name: "binary installation failed", banner: "\nPhase 2b: Installing/updating binaries...\n", run: o.ops.installBinaries},
	)
}

// postSwapSteps run under the NEW binary, in order, before Phase 5 restarts
// orama-node.
//
//   - Tor, in "ensure" form: with Tor installed and its repository current it
//     only re-masks the distro units and rewrites the torrc.
//   - The privileged helper (binary, orama-privhelper.socket and its
//     per-connection service): orama-node's first act after the restart is
//     starting @index units through the helper's socket.
//   - Copied signing keys: orama-node itself moves the pre-0.200 layout when
//     it next starts (pkg/legacylayout). The one thing the node may be unable
//     to move is the index gateway's signing keys in secrets/, which install
//     writes as root: it copies those and records each copy, and this step
//     deletes exactly the recorded originals. On the upgrade that crosses the
//     layouts there is nothing to delete yet; the next upgrade removes them.
//   - Templates before the services that use them: orama-node's first act is
//     starting orama-namespace-wireguard@index.
func (o *Orchestrator) postSwapSteps() []upgradeStep {
	return []upgradeStep{
		{name: "secret generation failed", banner: "\n🔐 Phase 3: Ensuring secrets...\n", run: o.ops.secrets},
		{name: "config generation failed (configs left unchanged)", run: o.ops.configs},
		{name: "service initialization failed", banner: "\nPhase 2c: Ensuring services are properly initialized...\n", run: o.ops.initServices},
		{name: "tor setup failed", banner: "\nPhase 2d: Ensuring the Tor client...\n", run: o.ops.torEnsure},
		{name: "privileged helper", banner: "\n🔑 Ensuring the privileged helper...\n", run: o.ops.privHelper},
		{name: "removing copied signing keys failed", banner: "\n🔑 Removing signing keys orama-node copied out of secrets/...\n", run: o.ops.removeKeys},
		{name: "namespace template installation failed", banner: "\n🔧 Phase 4b: Installing namespace systemd templates...\n", run: o.ops.templates},
		// Fatal. The unit files are what the supervisor runs; continuing past
		// a failure here means restarting into the old ones.
		{name: "systemd service update failed", banner: "\n🔧 Phase 5: Updating systemd services...\n", run: o.ops.systemdUnits},
		// Fatal. A wrong rule set is a node exposed to the internet or one
		// partitioned from the overlay.
		{name: "firewall reconcile failed", banner: "\n🛡️  Reconciling firewall rules...\n", run: o.ops.firewall},
	}
}

// restartSteps bring the node back. The maintenance flag the hand-over wrote
// is cleared last, once the node serves again; a failure before that leaves it
// set, which is what keeps a node that did not come back out of rotation.
func (o *Orchestrator) restartSteps() []upgradeStep {
	return []upgradeStep{
		{name: "the node did not come back", run: o.ops.restart},
		{name: "retiring 0.122.x deployment units failed", banner: "\n🧹 Retiring pre-template deployment units...\n", run: o.ops.retireLegacy},
		{name: "clearing the maintenance flag failed", run: o.ops.clearMaintMode},
	}
}

// Execute runs the upgrade process
func (o *Orchestrator) Execute() error {
	fmt.Printf("🔄 Upgrading production installation...\n")
	if o.flags.ReexecedAfterBinarySwap {
		fmt.Printf("  (Resumed under newly-installed binary — bug #15 chicken-and-egg fix.)\n")
		fmt.Printf("  Skipping the steps before the binary swap (already done by the previous process).\n")
	} else {
		fmt.Printf("  This will preserve existing configurations and data\n")
		fmt.Printf("  Configurations will be updated to latest format\n\n")

		if err := runSteps(o.preStopSteps(), "no services were stopped"); err != nil {
			return err
		}
		if err := runSteps(o.swapSteps(), ""); err != nil {
			return err
		}
		// Up to here this is whatever binary the operator ran. The phases
		// below regenerate configs and units, so they must be the new
		// release's code: the just-installed binary takes over from here
		// (syscall.Exec — control does not return on success). A binary
		// that is already the installed one has nothing to hand over to.
		// A failure is fatal: carrying on would write this release's
		// configs with another release's code.
		if err := o.ops.reexec(); err != nil {
			return fmt.Errorf("could not hand over to the newly installed binary (the node's services are stopped; "+
				"re-run `orama node upgrade --restart` on it): %w", err)
		}
	}

	if err := runSteps(o.postSwapSteps(), ""); err != nil {
		return err
	}

	// The success line comes after the restart, the riskiest step.
	if o.flags.RestartServices {
		if err := runSteps(o.restartSteps(), ""); err != nil {
			return err
		}
		fmt.Printf("\n✅ Upgrade complete!\n")
		return nil
	}

	fmt.Printf("\n✅ Upgrade staged. Services have NOT been restarted.\n")
	fmt.Printf("   To apply changes:\n")
	fmt.Printf("   sudo orama node restart\n\n")
	return nil
}

func (o *Orchestrator) handleBranchPreferences() error {
	prefs := oramainstall.LoadPreferences(o.oramaDir)

	if o.setup.IsNameserver() {
		fmt.Printf("  Nameserver mode: enabled (CoreDNS + Caddy)\n")
	}
	if o.flags.Nameserver == nil {
		return nil
	}
	// Fatal: the preference is what every later install and upgrade reads to
	// decide whether this node is a nameserver.
	prefs.Nameserver = *o.flags.Nameserver
	if err := oramainstall.SavePreferences(o.oramaDir, prefs); err != nil {
		return fmt.Errorf("save the nameserver preference: %w", err)
	}
	return nil
}

// resolveAndSetPublicIP decides node.public_ip (resolvePublicIP) and records
// it for Phase 4 — and for the post-swap process, which is handed the result
// with --public-ip rather than resolving it a second time.
func (o *Orchestrator) resolveAndSetPublicIP() error {
	publicIP, err := resolvePublicIP(o.flags.PublicIP, o.setup.PublicIP, routeSourceIP)
	if err != nil {
		return err
	}
	o.flags.PublicIP = publicIP
	o.setup.SetPublicIP(publicIP)
	fmt.Printf("  node.public_ip: %s\n", publicIP)
	return nil
}

// initializeServices is Phase 2c.
func (o *Orchestrator) initializeServices() error {
	peers := o.extractPeers()
	vpsIP, _ := o.extractNetworkConfig()
	return o.setup.Phase2cInitializeServices(peers, vpsIP, nil, nil)
}
