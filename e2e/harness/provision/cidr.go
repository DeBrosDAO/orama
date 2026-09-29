package provision

import (
	"fmt"
	"net"
	"strings"
)

// anywhereCIDRs is the SSH source of E2E_ALLOW_OPEN_SSH=1.
const anywhereCIDRs = "0.0.0.0/0,::/0"

// allowOpenSSHOn is the E2E_ALLOW_OPEN_SSH value that lets SSH in from
// anywhere.
const allowOpenSSHOn = "1"

// runnerCIDRs are the sources the firewall lets reach SSH: E2E_RUNNER_CIDR.
// The runner does not look its own address up (that would mean asking a
// third party); without the variable the run is refused unless
// E2E_ALLOW_OPEN_SSH=1 opens SSH to anywhere on purpose.
func runnerCIDRs() ([]string, error) {
	if v := envOr(EnvRunnerCIDR, ""); v != "" {
		return splitList(v), nil
	}
	if envOr(EnvAllowOpenSSH, "") == allowOpenSSHOn {
		return splitList(anywhereCIDRs), nil
	}
	return nil, fmt.Errorf("%s is not set: set it to the public address this runner's traffic leaves from "+
		"(for example %s=203.0.113.7/32; behind NAT, the NAT's address), or set %s=%s to let SSH in from anywhere",
		EnvRunnerCIDR, EnvRunnerCIDR, EnvAllowOpenSSH, allowOpenSSHOn)
}

// checkCIDRs refuses an empty list or an entry that is not a CIDR.
func checkCIDRs(cidrs []string) error {
	if len(cidrs) == 0 {
		return fmt.Errorf("%s must name at least one CIDR", EnvRunnerCIDR)
	}
	for _, c := range cidrs {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return fmt.Errorf("%s entry %q is not a CIDR: %w", EnvRunnerCIDR, c, err)
		}
	}
	return nil
}

// splitList splits a comma-separated list, dropping blanks.
func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
