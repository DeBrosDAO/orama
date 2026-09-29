package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

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
}

// planNetns runs the checks a co-located install needs before the host
// changes: the machine can hold the layout, a cluster node is installed, and
// the machine is not already global-only.
func planNetns(h NetnsHost, opts GlobalInstallOptions) (*netnsPlan, error) {
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
	layout := globalnetns.Layout{Ports: opts.firewall().Ports(), Tools: tools}
	if err := layout.Validate(); err != nil {
		return nil, err
	}
	return &netnsPlan{layout: layout, prefs: prefs}, nil
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

// writeNetns writes the namespace's files and unit, then records the layout in
// preferences. Preferences come last: the node accepts role both only when the
// files they name exist.
func writeNetns(h GlobalHost, plan *netnsPlan) error {
	n := h.Netns
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
// rule lets the namespace's own traffic out, and one per port lets the
// DNAT'd connection in. spec is "<port>/<proto>".
func netnsRouteArgs(specs []string) [][]string {
	args := [][]string{{"route", "allow", "in", "on", globalnetns.HostIface, "comment", GlobalRuleComment}}
	for _, spec := range specs {
		port, proto, _ := strings.Cut(spec, "/")
		args = append(args, []string{"route", "allow", "proto", proto, "to", globalnetns.NSAddr, "port", port, "comment", GlobalRuleComment})
	}
	return args
}
