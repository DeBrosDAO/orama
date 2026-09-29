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
)

// GlobalServiceOrder is the start order. The chain is first: every other
// service reaches it only through its loopback RPC. The public Kubo comes
// before the provider, which pins through it. Stop runs it backwards.
var GlobalServiceOrder = []GlobalService{
	GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider, GlobalServiceArchiver, GlobalServiceIndexer, GlobalServiceRepair,
}

// Binaries the global units run, by their name in GlobalBinDir and in the
// operator's staged directory.
const (
	globalOramadBinary = constants.ChainDaemonName
	globalServiceBin   = "orama-global"
	// globalOramaCLI is the orama CLI, installed beside oramad so the chain
	// unit can run the sign-floor check (GlobalSignFloorCheck).
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
	// timers are the units a timer-driven companion of the service needs
	// started, enabled and stopped with it.
	timers []string
}

// globalIPFSGCUnit and globalIPFSGCTimer are the public Kubo's GC oneshot and its timer.
const (
	globalIPFSGCUnit  = "orama-global-ipfs-gc.service"
	globalIPFSGCTimer = "orama-global-ipfs-gc.timer"
)

var globalServiceSpecs = map[GlobalService]globalServiceSpec{
	GlobalServiceChain:    {unit: constants.ChainServiceUnit, user: globalChainUser, binaries: []string{globalOramadBinary, globalOramaCLI}},
	GlobalServiceIPFS:     {unit: constants.GlobalIPFSUnit, user: globalIPFSUser, binaries: []string{globalKuboBinary}, groups: []string{globalIPFSRPCGroup}, timers: []string{globalIPFSGCTimer}},
	GlobalServiceProvider: {unit: constants.GlobalProviderUnit, user: globalProviderUser, binaries: []string{globalServiceBin}, groups: []string{globalIPFSRPCGroup}},
	GlobalServiceArchiver: {unit: constants.GlobalArchiverUnit, user: globalArchiverUser, binaries: []string{globalServiceBin}},
	GlobalServiceIndexer:  {unit: constants.GlobalIndexerUnit, user: globalIndexerUser, binaries: []string{globalServiceBin}},
	GlobalServiceRepair:   {unit: constants.GlobalRepairUnit, user: globalRepairUser, binaries: []string{globalServiceBin}},
}

// GlobalServiceUnit is the systemd unit of s.
func GlobalServiceUnit(s GlobalService) string { return globalServiceSpecs[s].unit }

// GlobalServiceTimers are the timer units that run with s: started after it,
// stopped before it. Only the public Kubo has one (its GC).
func GlobalServiceTimers(s GlobalService) []string { return globalServiceSpecs[s].timers }

// ParseGlobalServices turns --services into a set in GlobalServiceOrder. The
// chain must be in it: the other services have no RPC but the local chain's.
// A provider pins through this host's public Kubo, so it needs ipfs beside it.
// A repair delegate holds repair seeds and never runs beside a provider.
func ParseGlobalServices(names []string) ([]GlobalService, error) {
	seen := map[GlobalService]bool{}
	for _, raw := range names {
		s := GlobalService(strings.TrimSpace(raw))
		if _, ok := globalServiceSpecs[s]; !ok {
			return nil, fmt.Errorf("unknown global service %q (known: chain, ipfs, provider, archiver, indexer, repair)", raw)
		}
		seen[s] = true
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("no global service named; --services takes chain[,ipfs,provider,archiver,indexer,repair]")
	}
	if !seen[GlobalServiceChain] {
		return nil, fmt.Errorf("the other global services reach the chain only on this host's loopback RPC; add chain to --services")
	}
	if seen[GlobalServiceProvider] && !seen[GlobalServiceIPFS] {
		return nil, fmt.Errorf("the provider pins public deals through this host's public Kubo; add ipfs to --services")
	}
	if seen[GlobalServiceProvider] && seen[GlobalServiceRepair] {
		return nil, fmt.Errorf("a repair delegate holds repair seeds and must not run beside a storage provider; install them on different hosts")
	}
	var out []GlobalService
	for _, s := range GlobalServiceOrder {
		if seen[s] {
			out = append(out, s)
		}
	}
	return out, nil
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
			{name: globalIPFSGCUnit, body: RenderGlobalIPFSGCUnit()},
			{name: globalIPFSGCTimer, body: RenderGlobalIPFSGCTimer(), enable: true},
		}
	case GlobalServiceProvider:
		main.body = RenderGlobalProviderUnit()
	case GlobalServiceArchiver:
		main.body = RenderGlobalArchiverUnit()
	case GlobalServiceIndexer:
		main.body = RenderGlobalIndexerUnit()
	default:
		main.body = RenderGlobalRepairUnit()
	}
	return []globalUnitFile{main}
}

// unitFilesFor is unitFiles as installed: inside the orama-global network
// namespace when the install is co-located. Every service and the public
// Kubo's GC oneshot join it (the GC calls Kubo's loopback RPC, which lives
// there); a timer runs no process of its own and stays in the root namespace.
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
		files[i].body = body
	}
	return files, nil
}
