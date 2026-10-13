package install

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/hardening"
)

const (
	// IPv6DisableSysctlPath is the sysctl drop-in that turns IPv6 off at every boot.
	// `orama node wipe` removes it.
	IPv6DisableSysctlPath = "/etc/sysctl.d/99-orama-disable-ipv6.conf"
	// RAMHygieneSysctlPath is the sysctl drop-in persistRAMHygiene writes (suid dumps,
	// core pattern, ptrace scope). `orama node wipe --nuclear` removes it.
	RAMHygieneSysctlPath = "/etc/sysctl.d/99-orama-ram-hygiene.conf"

	// PriorSysctlPrefix starts the comment lines (`# prior: <key>=<value>`) at the end of
	// each drop-in that hold the value the kernel had before Orama set the key. sysctl
	// ignores comments; the wipe reads them to put the values back, and they go with the
	// file they describe, so no other record is left behind.
	PriorSysctlPrefix = "# prior: "
)

// IPv6DisableSysctls are the keys IPv6DisableSysctlPath sets (Want is the value it sets).
var IPv6DisableSysctls = []hardening.Sysctl{
	{Key: "net.ipv6.conf.all.disable_ipv6", Path: "/proc/sys/net/ipv6/conf/all/disable_ipv6", Want: "1"},
	{Key: "net.ipv6.conf.default.disable_ipv6", Path: "/proc/sys/net/ipv6/conf/default/disable_ipv6", Want: "1"},
}

// ipv6DisableDropIn renders the drop-in from IPv6DisableSysctls.
func ipv6DisableDropIn() string {
	var b strings.Builder
	b.WriteString("# Orama Network: disable IPv6 (no ip6tables rules configured)\n")
	for _, s := range IPv6DisableSysctls {
		fmt.Fprintf(&b, "%s = %s\n", s.Key, s.Want)
	}
	return b.String()
}

// withPriorValues is base followed by the prior-value lines of dropIn. A drop-in already on
// the machine keeps the lines it has (a second install finds Orama's own values in the
// kernel and must not record them as the machine's); one written by an install that
// recorded nothing stays without. A new drop-in gets the live value of each key, read
// before Orama sets it. A key the kernel does not have is skipped: there is nothing to put
// back.
func withPriorValues(dropIn, base string, sysctls []hardening.Sysctl, readFile func(string) ([]byte, error)) (string, error) {
	var b strings.Builder
	b.WriteString(base)
	existing, err := readFile(dropIn)
	switch {
	case err == nil:
		for _, line := range strings.Split(string(existing), "\n") {
			if strings.HasPrefix(line, PriorSysctlPrefix) {
				b.WriteString(line + "\n")
			}
		}
		return b.String(), nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("failed to read %s: %w", dropIn, err)
	}
	for _, s := range sysctls {
		raw, err := readFile(s.Path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("failed to read %s before Orama sets %s: %w", s.Path, s.Key, err)
		}
		fmt.Fprintf(&b, "%s%s=%s\n", PriorSysctlPrefix, s.Key, strings.TrimSpace(string(raw)))
	}
	return b.String(), nil
}
