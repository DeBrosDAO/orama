package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"gopkg.in/yaml.v3"
)

const (
	// netnsFileMode is the mode of the rulesets and resolv.conf: root's,
	// readable by the units that bind resolv.conf into their mount namespace.
	netnsFileMode = 0o644
	// netnsDirMode is /etc/orama-global.
	netnsDirMode = 0o755
	// priorForwardLimit bounds the recorded sysctl value read back.
	priorForwardLimit = 16
	// chainClientsLimit bounds the recorded account list read back.
	chainClientsLimit = 4096

	// roleCluster, roleGlobal and roleBoth are the values preferences.yaml
	// records; they are boot.Role's strings without importing the node package.
	roleGlobal = "global"
	roleBoth   = "both"
)

// NetnsHost is what a --colocated install touches beyond a global-only one.
// DefaultNetnsHost is this machine; a test points it at a temporary directory.
type NetnsHost struct {
	// Probe checks the machine can hold the layout.
	Probe globalnetns.Host
	// Root anchors ConfigDir and SysctlFile, which are below /etc.
	Root       rootfs.Root
	ConfigDir  string
	SysctlFile string
	// OramaDir holds the cluster node's preferences.yaml.
	OramaDir string
}

// DefaultNetnsHost is this machine.
func DefaultNetnsHost(run commandRunner) NetnsHost {
	return NetnsHost{
		Probe: globalnetns.Host{
			GOOS: runtime.GOOS, Run: run, LookPath: exec.LookPath,
			Exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		},
		Root:       rootfs.At("/etc"),
		ConfigDir:  globalnetns.ConfigDir,
		SysctlFile: globalnetns.SysctlFile,
		OramaDir:   OramaDir,
	}
}

// netnsPlan is what a --colocated install decided before it changed anything.
type netnsPlan struct {
	layout globalnetns.Layout
	prefs  *NodePreferences
	// clientUsers are the extra accounts allowed to reach the host-only ports, by name: the
	// persisted set plus this install's, sorted. They are saved once the layout is written.
	clientUsers []string
}

// planNetns runs the checks a co-located install needs before the host
// changes: the machine can hold the layout, a cluster node is installed, and
// the machine is not already global-only.
func planNetns(g GlobalHost, opts GlobalInstallOptions) (*netnsPlan, error) {
	h := g.Netns
	if err := globalnetns.InstallTools(h.Probe); err != nil {
		return nil, fmt.Errorf("this machine cannot share a cluster node with global services: %w", err)
	}
	tools, err := globalnetns.Preflight(h.Probe)
	if err != nil {
		return nil, fmt.Errorf("this machine cannot share a cluster node with global services: %w", err)
	}
	prefs, err := readClusterPreferences(h)
	if err != nil {
		return nil, err
	}
	if prefs.Role == roleGlobal {
		return nil, fmt.Errorf("this machine's role is global: it has no cluster node to share with; drop --colocated")
	}
	layout := globalnetns.Layout{Ports: opts.firewall().Ports(), HostPorts: opts.hostPorts(), Tools: tools}
	var users []string
	if len(layout.HostPorts) > 0 {
		uid, _, err := g.Lookup(supervisorUser)
		if err != nil {
			return nil, fmt.Errorf("cannot find the %s account the cluster node runs as, which is allowed to reach the chain's host-only ports: %w", supervisorUser, err)
		}
		layout.HostClientUIDs = []int{uid}
		if users, err = chainClientUsers(g, opts); err != nil {
			return nil, err
		}
		for _, name := range users {
			cuid, _, err := g.Lookup(name)
			if err != nil {
				return nil, fmt.Errorf("--chain-client-user %q: no such account: %w", name, err)
			}
			if cuid == 0 {
				return nil, fmt.Errorf("--chain-client-user %q is root, which is always allowed", name)
			}
			layout.HostClientUIDs = append(layout.HostClientUIDs, cuid)
		}
	}
	if err := layout.Validate(); err != nil {
		return nil, err
	}
	return &netnsPlan{layout: layout, prefs: prefs, clientUsers: users}, nil
}

// readClusterPreferences reads the cluster node's preferences.yaml. Its
// absence means no cluster node was installed here, which a co-located
// install refuses: `orama node setup` writes the file, and would overwrite the
// co-located role if it ran after.
func readClusterPreferences(h NetnsHost) (*NodePreferences, error) {
	path := h.OramaDir + "/" + preferencesFile
	data, err := OramaRoot(h.OramaDir).ReadFile(path, rootfs.SmallFileLimit)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no cluster node is installed here (%s is missing): run orama node setup first, then orama global install --colocated; a machine with no cluster node installs the global services without --colocated", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var prefs NodePreferences
	if err := yaml.Unmarshal(data, &prefs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &prefs, nil
}

// refuseGlobalOnlyOnColocated stops a plain install from rewriting a
// co-located machine's units back into the root namespace, where the global
// services would share the cluster's loopback and ports.
func refuseGlobalOnlyOnColocated(h NetnsHost) error {
	data, err := OramaRoot(h.OramaDir).ReadFile(h.OramaDir+"/"+preferencesFile, rootfs.SmallFileLimit)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", h.OramaDir+"/"+preferencesFile, err)
	}
	var prefs NodePreferences
	if err := yaml.Unmarshal(data, &prefs); err != nil {
		return fmt.Errorf("parse %s: %w", h.OramaDir+"/"+preferencesFile, err)
	}
	if prefs.Role == roleBoth {
		return fmt.Errorf("this machine is co-located (role both): its global services run in the %s network namespace; re-run with --colocated", globalnetns.Name)
	}
	return nil
}

var chainClientUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// chainClientUsers is the persisted set of extra chain client accounts joined with this install's,
// sorted and without repeats. A re-install without the flag keeps what an earlier one allowed.
func chainClientUsers(g GlobalHost, opts GlobalInstallOptions) ([]string, error) {
	path := filepath.Join(g.StateDir, constants.GlobalNetnsChainClientsFile)
	names := slices.Clone(opts.ChainClientUsers)
	data, err := g.StateRoot.ReadFile(path, chainClientsLimit)
	switch {
	case err == nil:
		names = append(names, strings.Fields(string(data))...)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	for _, n := range names {
		if !chainClientUserPattern.MatchString(n) {
			return nil, fmt.Errorf("chain client account %q is not a plain account name", n)
		}
	}
	return names, nil
}

func saveChainClientUsers(h GlobalHost, users []string) error {
	path := filepath.Join(h.StateDir, constants.GlobalNetnsChainClientsFile)
	body := strings.Join(users, "\n")
	if body != "" {
		body += "\n"
	}
	if err := h.StateRoot.WriteFile(path, []byte(body), globalUnitMode); err != nil {
		return fmt.Errorf("record the chain client accounts in %s: %w", path, err)
	}
	return nil
}

// recordPriorForwarding saves the value net.ipv4.ip_forward has before the layout turns it on, in
// the global state directory, once: a second install (or one that follows a partial one) finds the
// layout's own 1 in the kernel and must not overwrite what the machine had. Removing the layout
// puts the recorded value back.
func recordPriorForwarding(h GlobalHost, plan *netnsPlan) error {
	path := filepath.Join(h.StateDir, constants.GlobalNetnsPriorForwardFile)
	if _, err := h.StateRoot.ReadFile(path, priorForwardLimit); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	out, err := h.Run(plan.layout.Tools.Sysctl, "-n", "net.ipv4.ip_forward")
	if err != nil {
		return fmt.Errorf("read net.ipv4.ip_forward before the layout turns it on: %w: %s", err, strings.TrimSpace(string(out)))
	}
	value := strings.TrimSpace(string(out))
	if value != "0" && value != "1" {
		return fmt.Errorf("net.ipv4.ip_forward reads %q, want 0 or 1", value)
	}
	if err := h.StateRoot.WriteFile(path, []byte(value+"\n"), globalUnitMode); err != nil {
		return fmt.Errorf("record the prior net.ipv4.ip_forward in %s: %w", path, err)
	}
	return nil
}

// writeNetns writes the namespace's files and unit, then records the layout in
// preferences. Preferences come last: the node accepts role both only when the
// files they name exist.
func writeNetns(h GlobalHost, plan *netnsPlan) error {
	n := h.Netns
	if err := recordPriorForwarding(h, plan); err != nil {
		return err
	}
	if len(plan.layout.HostPorts) > 0 {
		if err := saveChainClientUsers(h, plan.clientUsers); err != nil {
			return err
		}
	}
	for _, dir := range []string{n.ConfigDir, filepath.Dir(n.SysctlFile)} {
		if err := n.Root.MkdirAll(dir, netnsDirMode); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	files := []struct {
		path string
		data string
	}{
		{n.ConfigDir + "/netns-host.nft", plan.layout.RenderHostRules()},
		{n.ConfigDir + "/netns.nft", plan.layout.RenderNSRules()},
		{n.ConfigDir + "/resolv.conf", globalnetns.RenderResolvConf()},
		{n.SysctlFile, globalnetns.RenderSysctl()},
	}
	for _, f := range files {
		if err := n.Root.WriteFile(f.path, []byte(f.data), netnsFileMode); err != nil {
			return fmt.Errorf("write %s: %w", f.path, err)
		}
	}
	path := h.UnitDir + "/" + globalnetns.UnitName
	if err := h.UnitRoot.WriteFile(path, []byte(plan.layout.RenderUnit()), globalUnitMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// savePreferencesBoth records role both and the namespace.
func savePreferencesBoth(n NetnsHost, prefs *NodePreferences) error {
	prefs.Role = roleBoth
	prefs.GlobalNetns = globalnetns.Name
	return SavePreferences(n.OramaDir, prefs)
}

// Ports is the public listeners as protocol and port.
func (g GlobalFirewall) Ports() []globalnetns.Port {
	var out []globalnetns.Port
	for _, spec := range NewFirewallProvisioner(FirewallConfig{Global: g}).globalPortSpecs() {
		number, proto, _ := strings.Cut(spec, "/")
		n, err := strconv.Atoi(number)
		if err != nil {
			continue
		}
		out = append(out, globalnetns.Port{Proto: proto, Number: n})
	}
	return out
}

// netnsRouteArgs are the ufw argv for a namespace's published ports: one
// rule lets the namespace's own traffic out (forwarded only when it comes from
// the namespace's address), and one per port lets the DNAT'd connection in.
// spec is "<port>/<proto>".
//
// The first argv removes the rule earlier releases added, which allowed
// forwarding in from ogl-host for any source. ufw reports success when there
// is no such rule, so it is safe on a machine that never had it.
func netnsRouteArgs(specs []string) [][]string {
	args := [][]string{
		{"route", "delete", "allow", "in", "on", globalnetns.HostIface},
		{"route", "allow", "in", "on", globalnetns.HostIface, "from", globalnetns.NSAddr, "comment", GlobalRuleComment},
	}
	for _, spec := range specs {
		port, proto, _ := strings.Cut(spec, "/")
		args = append(args, []string{"route", "allow", "proto", proto, "to", globalnetns.NSAddr, "port", port, "comment", GlobalRuleComment})
	}
	return args
}

// hostPorts are the loopback listeners that move to the namespace address on
// a co-located machine, for the services installed: the chain's RPC and REST
// API, and the indexer's read API. The host's gateway and node report read
// them there.
func (o GlobalInstallOptions) hostPorts() []int {
	var ports []int
	if slices.Contains(o.Services, GlobalServiceChain) {
		ports = append(ports, constants.ChainRPCPort, constants.ChainAPIPort)
	}
	if slices.Contains(o.Services, GlobalServiceIndexer) {
		ports = append(ports, constants.GlobalIndexerPort)
	}
	return ports
}

// colocatedListeners moves a unit's loopback listeners for the chain's RPC,
// REST API and the indexer to the namespace address, and points the services
// that call the chain's RPC (the provider, archiver, repair delegate and
// indexer) at it. The chain's gRPC stays on loopback: only the chain's own
// tools use it. Every rewrite must match exactly once, so a template change
// that no longer has the flag fails the install instead of leaving a listener
// unreachable from the host.
func colocatedListeners(s GlobalService, body string) (string, error) {
	ns := constants.GlobalNetnsAddr
	rpcFlag := fmt.Sprintf("tcp://%s:%d", ns, constants.ChainRPCPort)
	var swaps [][2]string
	switch s {
	case GlobalServiceChain:
		swaps = [][2]string{
			{fmt.Sprintf("--rpc.laddr tcp://127.0.0.1:%d", constants.ChainRPCPort), "--rpc.laddr " + rpcFlag},
			{fmt.Sprintf("--api.address tcp://127.0.0.1:%d", constants.ChainAPIPort), fmt.Sprintf("--api.address tcp://%s:%d", ns, constants.ChainAPIPort)},
		}
	case GlobalServiceIndexer:
		swaps = [][2]string{
			{fmt.Sprintf("--rpc tcp://127.0.0.1:%d", constants.ChainRPCPort), "--rpc " + rpcFlag},
			{fmt.Sprintf("--listen 127.0.0.1:%d", constants.GlobalIndexerPort), fmt.Sprintf("--listen %s:%d", ns, constants.GlobalIndexerPort)},
		}
	case GlobalServiceProvider, GlobalServiceArchiver, GlobalServiceRepair:
		// These take the chain's RPC from --rpc, whose default is loopback.
		exec := mustExecStart(body)
		if exec == "" {
			return "", fmt.Errorf("the %s unit has no single ExecStart line", s)
		}
		// Appending a second --rpc would silently win or lose against the first depending on the
		// flag parser, so a template that already names one fails the install, as a chain unit
		// with no flag to move does.
		if hasRPCFlag(exec) {
			return "", fmt.Errorf("the %s unit already has an --rpc flag, so the namespace address cannot be added exactly once", s)
		}
		return strings.Replace(body, "ExecStart="+exec, "ExecStart="+exec+" --rpc "+rpcFlag, 1), nil
	default:
		return body, nil
	}
	for _, sw := range swaps {
		if strings.Count(body, sw[0]) != 1 {
			return "", fmt.Errorf("the %s unit has no %q to move to the namespace address", s, sw[0])
		}
		body = strings.Replace(body, sw[0], sw[1], 1)
	}
	return body, nil
}

// hasRPCFlag reports whether an ExecStart line already has the flag --rpc, as its own token
// (--rpc <addr> or --rpc=<addr>), not as the start of another flag such as --rpc.laddr.
func hasRPCFlag(exec string) bool {
	for _, tok := range strings.Fields(exec) {
		if tok == "--rpc" || strings.HasPrefix(tok, "--rpc=") {
			return true
		}
	}
	return false
}

// mustExecStart is the value of the unit's only ExecStart= line, or "".
func mustExecStart(body string) string {
	var found string
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, "ExecStart="); ok {
			if found != "" {
				return ""
			}
			found = v
		}
	}
	return found
}
