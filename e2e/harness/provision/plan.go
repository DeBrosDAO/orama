package provision

import (
	"context"
	"fmt"
	"strings"
)

// Action is one step Up takes, as Plan reports it.
type Action struct {
	Phase       string
	Description string
}

// phase is a named part of Up: what it will do (plan) and doing it (exec).
// Plan and Up walk the same list, so the dry run is the run.
type phase struct {
	name string
	plan func(cfg Config) []string
	exec func(r *run, ctx context.Context) error
}

// Plan lists, in order, everything Up would do for cfg. It has no side
// effect: nothing is read from or written to the network or the disk.
func Plan(cfg Config) ([]Action, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var out []Action
	for _, p := range phases() {
		for _, d := range p.plan(cfg) {
			out = append(out, Action{Phase: p.name, Description: d})
		}
	}
	return out, nil
}

// phases is Up's sequence.
func phases() []phase {
	return []phase{
		{"workdir", planWorkDir, (*run).makeWorkDir},
		{"preflight", planPreflight, (*run).preflight},
		{"binaries", planBinaries, (*run).buildBinaries},
		{"agent", planAgent, (*run).startTestAgent},
		{"archives", planArchives, (*run).buildArchives},
		{"ssh-key", planSSHKey, (*run).makeSSHKey},
		{"hetzner-access", planAccess, (*run).registerAccess},
		{"servers", planServers, (*run).createServers},
		{"host-keys", planHostKeys, (*run).pinHostKeys},
		{"environment", planEnvironment, (*run).addEnvironment},
		{"genesis", planGenesis, (*run).installGenesis},
		{"delegation", planDelegation, (*run).delegateGenesis},
		{"certificate", planCertificate, (*run).waitGenesisCertificate},
		{"joins", planJoins, (*run).joinNodes},
		{"health", planHealth, (*run).checkHealth},
		{"wireguard", planWireGuard, (*run).readWireGuardIPs},
		{"chain", planChain, (*run).deployChain},
		{"state", planState, (*run).writeState},
	}
}

func planWorkDir(cfg Config) []string {
	return []string{fmt.Sprintf("create %s and %s (0700)", cfg.WorkDir, cfg.ArtifactDir)}
}

func planPreflight(cfg Config) []string {
	out := []string{
		fmt.Sprintf("check Hetzner location %s and server type %s (>= %d cores, %d GB RAM, %d GB disk)",
			cfg.Location, cfg.ServerType, nodeMinCores, nodeMinMemoryGB, nodeMinDiskGB),
		fmt.Sprintf("check the project can hold %d more servers (limit %d) and has none labelled %s", serverCount(cfg), cfg.ServerLimit, runSelector(cfg.RunID)),
		"check " + cfg.CFZone + " holds no records under " + envName(cfg.RunID) + "." + cfg.CFZone,
	}
	if cfg.ProbeLocation != "" {
		out = append(out, "check probe location "+cfg.ProbeLocation)
	}
	return out
}

func planBinaries(cfg Config) []string {
	out := []string{"go build -tags localrepo ./cmd/orama in " + cfg.RepoRoot + "/core -> " + cfg.WorkDir + "/bin/orama"}
	if ref, ok := strings.CutPrefix(cfg.PreviousArchive, previousRefPrefix); ok {
		out = append(out, "git archive "+ref+" -> "+prevSourceDir+", go build its orama CLI -> bin/"+oramaPrevName)
	}
	return out
}

func planAgent(cfg Config) []string {
	return []string{"rw init a random wallet and start rw-agent-headless approving the orama CLI for " + strings.Join(agentCaps(), ",")}
}

func planArchives(cfg Config) []string {
	out := []string{"orama maint build --output " + archiveDir + "/" + headArchive + " --test-local-release-repo (signed through the test agent)"}
	switch ref, isRef := strings.CutPrefix(cfg.PreviousArchive, previousRefPrefix); {
	case isRef:
		out = append(out, "orama-prev build of "+ref+" --output "+archiveDir+"/"+prevArchive)
	case cfg.PreviousArchive != "":
		out = append(out, "verify "+cfg.PreviousArchive+" is signed by the test wallet")
	}
	return out
}

func planSSHKey(Config) []string {
	return []string{"generate the run's ed25519 SSH key and an empty known_hosts"}
}

func planAccess(cfg Config) []string {
	return []string{"register SSH key " + envName(cfg.RunID) + " and firewall " + envName(cfg.RunID) + " (labels " + runSelector(cfg.RunID) +
		", SSH from " + strings.Join(cfg.RunnerCIDRs, ",") + ")"}
}

func planServers(cfg Config) []string {
	var out []string
	for i := 0; i < nodeCount; i++ {
		out = append(out, fmt.Sprintf("generate a host key for, then create, server %s (%s, %s, %s)", serverName(cfg.RunID, nodeSuffix(i)), cfg.ServerType, cfg.Image, cfg.Location))
	}
	if cfg.ProbeLocation != "" {
		out = append(out, fmt.Sprintf("create probe server %s (%s, %s)", serverName(cfg.RunID, probeName), cfg.ServerType, cfg.ProbeLocation))
	}
	return out
}

func planHostKeys(Config) []string {
	return []string{"pin each server's generated ssh-ed25519 host key, then confirm its sshd presents exactly that key"}
}

func planEnvironment(cfg Config) []string {
	return []string{"orama network add " + envName(cfg.RunID) + " https://" + envName(cfg.RunID) + "." + cfg.CFZone + " --ca-file " + caFileName,
		"orama network use " + envName(cfg.RunID)}
}

func planGenesis(cfg Config) []string {
	return []string{"orama" + installCLISuffix(cfg) + " node setup --genesis on node-1 as nameserver with --acme-ca " + acmeCA + " and the " + installRelease(cfg) + " archive"}
}

func planDelegation(cfg Config) []string {
	return []string{"read the claimed nameserver slots (orama node dns delegation --json) and write NS/glue in " + cfg.CFZone}
}

func planCertificate(cfg Config) []string {
	return []string{"wait until node-1 serves a staging certificate for " + envName(cfg.RunID) + "." + cfg.CFZone}
}

func planJoins(cfg Config) []string {
	return []string{"orama" + installCLISuffix(cfg) + " node setup --join-via node-1 for node-2, then node-3 (" + installRelease(cfg) + " archive), updating the delegation after each"}
}

func planHealth(cfg Config) []string {
	return []string{"orama auth login", "poll orama monitor report --env " + envName(cfg.RunID) + " --json until all nodes are healthy"}
}

func planWireGuard(Config) []string {
	return []string{"read each node's wg0 address"}
}

func planChain(cfg Config) []string {
	return []string{fmt.Sprintf("%s up with CHAIN_ID=%s (epoch %s, min %s blocks)", chainScript, chainID(cfg.RunID), cfg.EpochDuration, cfg.EpochMinBlocks)}
}

func planState(cfg Config) []string {
	return []string{"write " + StatePath(cfg.WorkDir)}
}

// serverCount is how many servers Up creates.
func serverCount(cfg Config) int {
	if cfg.ProbeLocation != "" {
		return nodeCount + 1
	}
	return nodeCount
}

// installRelease names the release Up installs.
func installRelease(cfg Config) string {
	if cfg.InstallPrevious {
		return "previous"
	}
	return "HEAD"
}

// installCLISuffix marks the plan line of a CLI other than HEAD's.
func installCLISuffix(cfg Config) string {
	if cfg.InstallPrevious {
		return "-prev"
	}
	return ""
}
