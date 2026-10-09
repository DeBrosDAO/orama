package install

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
)

// GlobalService is one service `orama global install` puts on a node.
type GlobalService string

// The services the global installer knows. Each is one unit and one account.
const (
	GlobalServiceChain    GlobalService = "chain"
	GlobalServiceIPFS     GlobalService = "ipfs"
	GlobalServiceProvider GlobalService = "provider"
	GlobalServiceArchiver GlobalService = "archiver"
	GlobalServiceIndexer  GlobalService = "indexer"
	GlobalServiceRepair   GlobalService = "repair"
	// The Orama Tor network's roles (docs/TOR_NETWORK.md). A directory
	// authority is also a relay, so dirauth and relay are never installed
	// together; the exit role is relay with a policy, parsed by
	// ParseGlobalRoles.
	GlobalServiceDirauth GlobalService = "dirauth"
	GlobalServiceRelay   GlobalService = "relay"
	// GlobalServiceOnion is the validator's onion service: tor, and the tx
	// gate it forwards to.
	GlobalServiceOnion GlobalService = "onion"
	// GlobalServiceReporter is a directory authority's bandwidth reporter: it
	// reads the authority's votes and reports each closed epoch to x/relay
	// through this host's chain. It goes beside dirauth and chain.
	GlobalServiceReporter GlobalService = "reporter"
)

// globalExitRole is the --services word that makes the relay an exit.
const globalExitRole = "exit"

// GlobalServiceOrder is the start order. The chain is first: every other
// service reaches it only through its loopback RPC. The public Kubo comes
// before the provider, which pins through it. Stop runs it backwards.
var GlobalServiceOrder = []GlobalService{
	GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider, GlobalServiceArchiver, GlobalServiceIndexer, GlobalServiceRepair,
	GlobalServiceDirauth, GlobalServiceRelay, GlobalServiceOnion, GlobalServiceReporter,
}

// Binaries the global units run, by their name in GlobalBinDir and in the
// operator's staged directory.
const (
	globalOramadBinary = constants.ChainDaemonName
	globalServiceBin   = "orama-global"
	// globalOramaCLI is the orama CLI, installed beside oramad so the chain
	// unit can run the sign-floor check (GlobalSignFloorCheck), and beside the
	// public Kubo for its GC unit (`orama node ipfs-gc`).
	globalOramaCLI = "orama"
	// globalKuboBinary is the public Kubo daemon, the release's ipfs at constants.IPFSKuboVersion.
	globalKuboBinary = "ipfs"
)

// globalIPFSRPCGroup is the group that may read the public Kubo RPC token.
// The provider unit names it as a supplementary group, and systemd refuses
// to start a unit whose group does not exist.
const globalIPFSRPCGroup = "orama-ipfs-pub-rpc"

type globalServiceSpec struct {
	unit     string
	user     string
	binaries []string
	// groups are system groups the unit names besides its user's own.
	groups []string
	// users are accounts of the service's companion units besides user.
	users []string
	// companions are the units that run with the service (a timer that fires
	// a oneshot, the tx gate behind the onion service): started after it,
	// stopped before it.
	companions []string
	// standalone says the service never reaches the chain: a Tor relay or
	// directory authority runs without one, and a chain restart leaves it alone.
	// Every other service uses the chain's loopback RPC or REST API, so it
	// starts after the chain and the chain must be installed.
	standalone bool
}

// globalIPFSGCUnit and globalIPFSGCTimer are the public Kubo's GC oneshot and its timer.
const (
	globalIPFSGCUnit  = "orama-global-ipfs-gc.service"
	globalIPFSGCTimer = "orama-global-ipfs-gc.timer"
)

var globalServiceSpecs = map[GlobalService]globalServiceSpec{
	GlobalServiceChain:    {unit: constants.ChainServiceUnit, user: globalChainUser, binaries: []string{globalOramadBinary, globalOramaCLI}},
	GlobalServiceIPFS:     {unit: constants.GlobalIPFSUnit, user: globalIPFSUser, binaries: []string{globalKuboBinary, globalOramaCLI}, groups: []string{globalIPFSRPCGroup}, companions: []string{globalIPFSGCTimer}},
	GlobalServiceProvider: {unit: constants.GlobalProviderUnit, user: globalProviderUser, binaries: []string{globalServiceBin}, groups: []string{globalIPFSRPCGroup}},
	GlobalServiceArchiver: {unit: constants.GlobalArchiverUnit, user: globalArchiverUser, binaries: []string{globalServiceBin}},
	GlobalServiceIndexer:  {unit: constants.GlobalIndexerUnit, user: globalIndexerUser, binaries: []string{globalServiceBin}},
	GlobalServiceRepair:   {unit: constants.GlobalRepairUnit, user: globalRepairUser, binaries: []string{globalServiceBin}},
	// The Tor roles run the distro tor binary. The dirauth's archive timer and the
	// onion service's gate run the orama CLI.
	GlobalServiceDirauth: {unit: constants.GlobalTorDirauthUnit, user: globalTorDirauthUser, binaries: []string{globalOramaCLI}, companions: []string{constants.GlobalTorArchiveTimer, constants.GlobalTorMonitorTimer}, standalone: true},
	GlobalServiceRelay:   {unit: constants.GlobalTorRelayUnit, user: globalTorRelayUser, binaries: []string{globalOramaCLI}, companions: []string{constants.GlobalTorMonitorTimer}, standalone: true},
	GlobalServiceOnion:   {unit: constants.GlobalTorOnionUnit, user: globalTorOnionUser, binaries: []string{globalOramaCLI}, users: []string{globalTxGateUser}, companions: []string{constants.GlobalTxGateUnit}},
	// The reporter is orama-global's reporter command; it reaches the chain's loopback RPC.
	GlobalServiceReporter: {unit: constants.GlobalReporterUnit, user: globalReporterUser, binaries: []string{globalServiceBin}},
}

// GlobalServiceUnit is the systemd unit of s.
func GlobalServiceUnit(s GlobalService) string { return globalServiceSpecs[s].unit }

// GlobalServiceCompanions are the units that run with s: started after it,
// stopped before it. The public Kubo has its GC timer, a directory authority
// its archive timer and its monitor timer, a relay its monitor timer, and the onion service its tx gate.
func GlobalServiceCompanions(s GlobalService) []string { return globalServiceSpecs[s].companions }

// GlobalServiceNeedsChain reports whether s uses the local chain: every service
// but the chain itself and the standalone Tor roles.
func GlobalServiceNeedsChain(s GlobalService) bool {
	return s != GlobalServiceChain && !globalServiceSpecs[s].standalone
}

// ParseGlobalServices turns --services into a set in GlobalServiceOrder. The
// chain must be in it unless every service is a standalone Tor role (relay,
// dirauth) or the onion service, which InstallGlobal lets join a machine whose
// chain is installed: the others reach the chain only on this host's loopback
// RPC. A directory-authority host runs dirauth alone, or with the chain and the
// reporter that reports its votes through that chain.
// A provider pins through this host's public Kubo, so it needs ipfs beside it.
// A repair delegate holds repair seeds and never runs beside a provider. A
// directory authority is a relay already, so it and relay never go together.
func ParseGlobalServices(names []string) ([]GlobalService, error) {
	seen := map[GlobalService]bool{}
	for _, raw := range names {
		s := GlobalService(strings.TrimSpace(raw))
		if _, ok := globalServiceSpecs[s]; !ok {
			return nil, fmt.Errorf("unknown global service %q (known: chain, ipfs, provider, archiver, indexer, repair, dirauth, relay, onion, reporter; exit goes beside relay)", raw)
		}
		seen[s] = true
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("no global service named; --services takes chain[,ipfs,provider,archiver,indexer,repair,dirauth,relay,onion,reporter]")
	}
	for _, s := range GlobalServiceOrder {
		// The onion service is added to a machine whose chain is already installed;
		// InstallGlobal checks that.
		if seen[s] && GlobalServiceNeedsChain(s) && s != GlobalServiceOnion && !seen[GlobalServiceChain] {
			return nil, fmt.Errorf("%s reaches the chain only on this host's loopback RPC; add chain to --services", s)
		}
	}
	if seen[GlobalServiceProvider] && !seen[GlobalServiceIPFS] {
		return nil, fmt.Errorf("the provider pins public deals through this host's public Kubo; add ipfs to --services")
	}
	if seen[GlobalServiceProvider] && seen[GlobalServiceRepair] {
		return nil, fmt.Errorf("a repair delegate holds repair seeds and must not run beside a storage provider; install them on different hosts")
	}
	if seen[GlobalServiceDirauth] && seen[GlobalServiceRelay] {
		return nil, fmt.Errorf("a directory authority is a relay: install dirauth or relay on a host, not both")
	}
	var out []GlobalService
	for _, s := range GlobalServiceOrder {
		if seen[s] {
			out = append(out, s)
		}
	}
	return out, nil
}

// ParseGlobalRoles is ParseGlobalServices plus the exit role. "exit" is not a
// service of its own: it makes the relay an exit, so it needs relay beside it
// and cannot go with dirauth.
func ParseGlobalRoles(names []string) (services []GlobalService, exit bool, err error) {
	var rest []string
	for _, raw := range names {
		if strings.TrimSpace(raw) == globalExitRole {
			exit = true
			continue
		}
		rest = append(rest, raw)
	}
	if services, err = ParseGlobalServices(rest); err != nil {
		return nil, false, err
	}
	if exit && !slices.Contains(services, GlobalServiceRelay) {
		return nil, false, fmt.Errorf("exit is a relay with an exit policy: use --services relay,exit")
	}
	return services, exit, nil
}

// ChainInit asks the installer to create the chain home. It is never done
// without one: an existing home is kept as it is.
type ChainInit struct {
	ChainID     string
	Moniker     string
	GenesisPath string
}

// GlobalInstallOptions is one `orama global install`.
type GlobalInstallOptions struct {
	Services        []GlobalService
	StagedDir       string
	PersistentPeers string
	InitChain       *ChainInit
	EnableFirewall  bool
	SSHPort         int
	// Colocated runs the services in the orama-global network namespace, so
	// the machine can also be a cluster node (role both).
	Colocated bool
	// PublicStorageBytes is the capacity the operator will declare for the
	// provider; the public Kubo's StorageMax is that plus headroom
	// (installers.PublicStorageMax). Required with the ipfs service.
	PublicStorageBytes uint64
	// ChainClientUsers are extra local accounts (besides root and the cluster node's account) that
	// may connect to the chain's host-only ports on a co-located machine: an operator's ssh login
	// that tunnels to the chain, or runs `orama chain`. Names are resolved to uids at install, an
	// unknown one refuses the install, and the set is kept in the state directory so a re-install
	// without the flag keeps it.
	ChainClientUsers []string
	// Tor is what the Tor roles (dirauth, relay, onion) and the reporter need beyond the service names.
	Tor TorOptions

	// exportVotes makes a directory authority's archive oneshot copy its own
	// vote to the votes directory: the host runs the reporter, in this install
	// or an earlier one (InstallGlobal sets it).
	exportVotes bool
}

var (
	persistentPeer = regexp.MustCompile(`^[0-9a-f]{40}@[A-Za-z0-9.-]{1,253}:[0-9]{1,5}$`)
	chainIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	monikerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ValidatePersistentPeers checks a comma-separated id@host:port list. It is
// written into the unit's ExecStart, so nothing else may pass.
func ValidatePersistentPeers(list string) error {
	if list == "" {
		return nil
	}
	for _, peer := range strings.Split(list, ",") {
		if !persistentPeer.MatchString(peer) {
			return fmt.Errorf("persistent peer %q is not <40 hex node id>@<host>:<port>", peer)
		}
	}
	return nil
}

// validate checks the options before anything on the host changes.
func (o GlobalInstallOptions) validate() error {
	if len(o.Services) == 0 {
		return fmt.Errorf("no global service to install")
	}
	if o.StagedDir == "" {
		return fmt.Errorf("the staged binary directory is required")
	}
	if err := ValidatePersistentPeers(o.PersistentPeers); err != nil {
		return err
	}
	if err := o.Tor.validate(o.Services); err != nil {
		return err
	}
	if slices.Contains(o.Services, GlobalServiceIPFS) && o.PublicStorageBytes == 0 {
		return fmt.Errorf("the public Kubo needs its storage budget: give the capacity you will declare with --public-storage-gb")
	}
	if o.SSHPort < 1 || o.SSHPort > 65535 {
		return fmt.Errorf("ssh port %d is not a TCP port", o.SSHPort)
	}
	if o.InitChain == nil {
		return nil
	}
	if !chainIDPattern.MatchString(o.InitChain.ChainID) {
		return fmt.Errorf("chain id %q must be lowercase letters, digits and dashes", o.InitChain.ChainID)
	}
	if !monikerPattern.MatchString(o.InitChain.Moniker) {
		return fmt.Errorf("moniker %q must be letters, digits, '.', '_' or '-'", o.InitChain.Moniker)
	}
	if o.InitChain.GenesisPath == "" {
		return fmt.Errorf("--genesis is required with --init-chain: a new node joins the network's genesis")
	}
	return nil
}

// binaries is the set of binaries the services need, sorted.
func (o GlobalInstallOptions) binaries() []string {
	var out []string
	for _, s := range o.Services {
		for _, b := range globalServiceSpecs[s].binaries {
			if !slices.Contains(out, b) {
				out = append(out, b)
			}
		}
	}
	slices.Sort(out)
	return out
}

// firewall is the public listeners of the services.
func (o GlobalInstallOptions) firewall() GlobalFirewall {
	return GlobalFirewall{
		ChainP2P:      slices.Contains(o.Services, GlobalServiceChain),
		PublicStorage: slices.Contains(o.Services, GlobalServiceIPFS),
		Provider:      slices.Contains(o.Services, GlobalServiceProvider),
		TorRelay:      slices.Contains(o.Services, GlobalServiceRelay),
		Dirauth:       slices.Contains(o.Services, GlobalServiceDirauth),
		Netns:         o.Colocated,
	}
}

// globalUnitFile is one file `orama global install` writes into the unit directory.
type globalUnitFile struct {
	name string
	body string
	// enable is false for a oneshot that only its timer starts: it has no
	// [Install] section to enable.
	enable bool
}

// unitFiles renders the unit files of s: its service, and for the public
// Kubo its GC oneshot and the timer that fires it.
func (o GlobalInstallOptions) unitFiles(s GlobalService) []globalUnitFile {
	main := globalUnitFile{name: globalServiceSpecs[s].unit, enable: true}
	switch s {
	case GlobalServiceChain:
		main.body = RenderGlobalChainUnit(o.PersistentPeers)
	case GlobalServiceIPFS:
		main.body = RenderGlobalIPFSUnit()
		return []globalUnitFile{
			main,
			{name: globalIPFSGCUnit, body: RenderGlobalIPFSGCUnit(globalnetns.KuboAPIHost(o.Colocated))},
			{name: globalIPFSGCTimer, body: RenderGlobalIPFSGCTimer(), enable: true},
		}
	case GlobalServiceProvider:
		main.body = RenderGlobalProviderUnit(globalnetns.KuboAPIHost(o.Colocated))
	case GlobalServiceArchiver:
		main.body = RenderGlobalArchiverUnit()
	case GlobalServiceIndexer:
		main.body = RenderGlobalIndexerUnit()
	case GlobalServiceDirauth:
		main.body = RenderGlobalTorDirauthUnit()
		return []globalUnitFile{
			main,
			{name: constants.GlobalTorArchiveUnit, body: RenderGlobalTorArchiveUnit(o.exportVotes)},
			{name: constants.GlobalTorArchiveTimer, body: RenderGlobalTorArchiveTimer(), enable: true},
			{name: constants.GlobalTorMonitorUnit, body: RenderGlobalTorDirauthMonitorUnit()},
			{name: constants.GlobalTorMonitorTimer, body: RenderGlobalTorMonitorTimer(), enable: true},
		}
	case GlobalServiceRelay:
		main.body = RenderGlobalTorRelayUnit()
		return []globalUnitFile{
			main,
			{name: constants.GlobalTorMonitorUnit, body: RenderGlobalTorMonitorUnit()},
			{name: constants.GlobalTorMonitorTimer, body: RenderGlobalTorMonitorTimer(), enable: true},
		}
	case GlobalServiceOnion:
		main.body = RenderGlobalTorOnionUnit()
		return []globalUnitFile{main, {name: constants.GlobalTxGateUnit, body: RenderGlobalTxGateUnit(), enable: true}}
	case GlobalServiceReporter:
		main.body = RenderGlobalReporterUnit()
	default:
		main.body = RenderGlobalRepairUnit()
	}
	return []globalUnitFile{main}
}

// unitFilesFor is unitFiles as installed: inside the orama-global network
// namespace when the install is co-located. Every service and the public
// Kubo's GC oneshot join it (the GC calls Kubo's RPC, which lives there); a timer runs no process of its own and stays in the root namespace.
func (o GlobalInstallOptions) unitFilesFor(s GlobalService) ([]globalUnitFile, error) {
	files := o.unitFiles(s)
	if !o.Colocated {
		return files, nil
	}
	for i := range files {
		if strings.HasSuffix(files[i].name, ".timer") {
			continue
		}
		body, err := globalnetns.ApplyToUnit(files[i].body)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", files[i].name, err)
		}
		if i == 0 && (s == GlobalServiceRelay || s == GlobalServiceDirauth) {
			// Not the archive oneshot: it is the authority's own CLI on its own files.
			if body, err = denyLoopback(body); err != nil {
				return nil, fmt.Errorf("render %s: %w", files[i].name, err)
			}
		}
		if i == 0 || files[i].name == constants.GlobalTxGateUnit {
			// The service's own unit (the tx gate is the onion service's listener);
			// the GC oneshot and the archive oneshot have no listener to move.
			if body, err = colocatedListeners(s, body); err != nil {
				return nil, fmt.Errorf("render %s: %w", files[i].name, err)
			}
		}
		files[i].body = body
	}
	return files, nil
}
