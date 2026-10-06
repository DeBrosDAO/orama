package install

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/hardening"
)

// disableApport stops and masks apport where it is installed, so nothing turns
// suid core dumps back on after the hardening sysctl. The unit's LoadState
// says whether it is there: not-found (nothing to do), masked (done), or
// anything else (mask it). A systemctl that cannot answer is an error, never
// read as "not installed".
func disableApport(run commandRunner) error {
	out, err := run("systemctl", "show", "-p", "LoadState", "--value", hardening.ApportUnit)
	if err != nil {
		return fmt.Errorf("failed to read whether %s is installed: %w\n%s", hardening.ApportUnit, err, out)
	}
	if hardening.ApportDisabled(strings.TrimSpace(string(out))) {
		return nil
	}
	if out, err := run("systemctl", "mask", "--now", hardening.ApportUnit); err != nil {
		return fmt.Errorf("failed to stop and mask %s, which turns suid core dumps back on at boot: %w\n%s", hardening.ApportUnit, err, out)
	}
	return nil
}

// verifyRAMHygiene fails unless the kernel reports the hardening in effect.
// The sysctl applying cleanly is not the proof: a writer that runs afterwards
// (apport did) changes the live value silently.
func verifyRAMHygiene(read func(string) ([]byte, error)) error {
	for _, v := range hardening.Sysctls {
		b, err := read(v.Path)
		if err != nil {
			return fmt.Errorf("failed to read %s after applying %s: %w", v.Path, ramHygieneSysctlPath, err)
		}
		if got := strings.TrimSpace(string(b)); got != v.Want {
			return fmt.Errorf("%s is %q after applying %s, want %q: another writer changed it (apport, /etc/sysctl.conf, a later sysctl.d file)", v.Path, got, ramHygieneSysctlPath, v.Want)
		}
	}
	return nil
}
