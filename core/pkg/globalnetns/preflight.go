package globalnetns

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// minSystemdVersion is the first systemd that has NetworkNamespacePath=.
const minSystemdVersion = 242

// probeNetns and probeVeth are the throwaway objects the machine check
// creates and removes to prove the kernel allows them.
const (
	probeNetns = "orama-netns-probe"
	probeVethA = "oglprobe0"
	probeVethB = "oglprobe1"
)

var systemdVersion = regexp.MustCompile(`^systemd (\d+)`)

// Host is what Preflight touches: the OS name, command execution, PATH lookup
// and file existence. A test supplies fakes.
type Host struct {
	GOOS     string
	Run      func(name string, args ...string) ([]byte, error)
	LookPath func(name string) (string, error)
	Exists   func(path string) bool
}

// Preflight decides whether this machine can hold the layout, before anything
// on it changes. It returns the binaries the namespace unit will run. Every
// refusal says what to install or change.
func Preflight(h Host) (Tools, error) {
	if h.GOOS != "linux" {
		return Tools{}, fmt.Errorf("network namespaces are a Linux feature; this machine is %s", h.GOOS)
	}
	if !h.Exists("/proc/self/ns/net") {
		return Tools{}, fmt.Errorf("this kernel has no network namespaces (/proc/self/ns/net is missing; CONFIG_NET_NS is off)")
	}
	tools, err := findTools(h)
	if err != nil {
		return Tools{}, err
	}
	if err := checkSystemd(h); err != nil {
		return Tools{}, err
	}
	if err := probeKernel(h, tools.IP); err != nil {
		return Tools{}, err
	}
	if err := checkAddressFree(h, tools.IP); err != nil {
		return Tools{}, err
	}
	return tools, nil
}

func findTools(h Host) (Tools, error) {
	hints := map[string]string{"ip": "iproute2", "nft": "nftables", "sysctl": "procps"}
	var paths []string
	for _, name := range []string{"ip", "nft", "sysctl"} {
		p, err := h.LookPath(name)
		if err != nil {
			return Tools{}, fmt.Errorf("%s is required for the network namespace and was not found: apt-get install -y %s", name, hints[name])
		}
		paths = append(paths, p)
	}
	return Tools{IP: paths[0], Nft: paths[1], Sysctl: paths[2]}, nil
}

func checkSystemd(h Host) error {
	out, err := h.Run("systemctl", "--version")
	if err != nil {
		return fmt.Errorf("systemctl --version: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	line, _, _ := strings.Cut(string(out), "\n")
	m := systemdVersion.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return fmt.Errorf("cannot read the systemd version from %q", line)
	}
	if v, _ := strconv.Atoi(m[1]); v < minSystemdVersion {
		return fmt.Errorf("systemd %d is too old: NetworkNamespacePath= needs %d or newer", v, minSystemdVersion)
	}
	return nil
}

// probeKernel creates a namespace and a veth pair and deletes them again. A
// kernel or container that forbids either fails here, not halfway through the
// install.
func probeKernel(h Host, ip string) (err error) {
	if out, err := h.Run(ip, "netns", "add", probeNetns); err != nil {
		return fmt.Errorf("cannot create a network namespace (%w); a container or a restricted kernel cannot host the co-located layout\n%s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		if out, delErr := h.Run(ip, "netns", "del", probeNetns); delErr != nil {
			err = errors.Join(err, fmt.Errorf("remove the probe namespace %s (delete it with: ip netns del %s): %w\n%s", probeNetns, probeNetns, delErr, strings.TrimSpace(string(out))))
		}
	}()
	if out, err := h.Run(ip, "link", "add", probeVethA, "type", "veth", "peer", "name", probeVethB); err != nil {
		return fmt.Errorf("cannot create a veth pair (%w); load the veth module (modprobe veth) or use a kernel that has it\n%s", err, strings.TrimSpace(string(out)))
	}
	if out, err := h.Run(ip, "link", "del", probeVethA); err != nil {
		return fmt.Errorf("remove the probe veth %s (delete it with: ip link del %s): %w\n%s", probeVethA, probeVethA, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// checkAddressFree refuses a machine that already routes the veth subnet
// somewhere other than our own interface.
func checkAddressFree(h Host, ip string) error {
	out, err := h.Run(ip, "-4", "route", "show")
	if err != nil {
		return fmt.Errorf("%s -4 route show: %w\n%s", ip, err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "198.18.") && !strings.Contains(line, "dev "+HostIface) {
			return fmt.Errorf("the address range %s is already routed on this machine (%q); the co-located layout needs it", Subnet, strings.TrimSpace(line))
		}
	}
	return nil
}

// Paths are the files that make a layout installed.
type Paths struct {
	Unit     string // the namespace unit
	HostFile string
	NSFile   string
	Resolv   string
}

// DefaultPaths is this machine's layout, with the unit in unitDir.
func DefaultPaths(unitDir string) Paths {
	return Paths{Unit: unitDir + "/" + UnitName, HostFile: HostRulesFile, NSFile: NSRulesFile, Resolv: ResolvFile}
}

// Verify checks that the layout named by recorded (preferences.yaml
// global_netns) is the one this build knows and that every file of it is in
// place. The node refuses to run as role "both" otherwise.
func Verify(recorded string, p Paths, exists func(string) bool) error {
	if recorded != Name {
		return fmt.Errorf("preferences record network namespace %q, want %q: run orama global install --colocated", recorded, Name)
	}
	for _, f := range []string{p.Unit, p.HostFile, p.NSFile, p.Resolv} {
		if !exists(f) {
			return fmt.Errorf("the network namespace layout is incomplete: %s is missing; run orama global install --colocated again", f)
		}
	}
	return nil
}

// Installed reports whether this machine's co-located layout is installed: the
// namespace unit is in unitDir. It is how a process outside the global
// services (the cluster gateway, the node report) learns that the chain's
// listeners are on NSAddr and not on loopback.
func Installed(unitDir string, exists func(string) bool) bool {
	return exists(DefaultPaths(unitDir).Unit)
}

// ChainHost is the address the chain's RPC and REST API and the indexer
// listen on: NSAddr when co-located, loopback otherwise.
func ChainHost(colocated bool) string {
	if colocated {
		return NSAddr
	}
	return "127.0.0.1"
}
