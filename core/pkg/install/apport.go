package install

import (
	"fmt"
	"strings"
)

// apportUnit is Ubuntu's crash reporter. Its start writes fs.suid_dumpable=2
// (/usr/share/apport/apport), and it starts after systemd-sysctl has applied
// ramHygieneSysctl, so every boot turned suid core dumps back on: a crashing
// setuid process could then dump its secret-bearing memory to disk (stagenet
// 2026-10-03, fs.suid_dumpable 2 after a reboot). Debian does not ship it.
const apportUnit = "apport.service"

// disableApport stops and masks apport where it is installed, so nothing turns
// suid core dumps back on after the hardening sysctl. The unit's LoadState
// says whether it is there: not-found (nothing to do), masked (done), or
// anything else (mask it). A systemctl that cannot answer is an error, never
// read as "not installed".
func disableApport(run commandRunner) error {
	out, err := run("systemctl", "show", "-p", "LoadState", "--value", apportUnit)
	if err != nil {
		return fmt.Errorf("failed to read whether %s is installed: %w\n%s", apportUnit, err, out)
	}
	switch strings.TrimSpace(string(out)) {
	case "not-found", "masked":
		return nil
	}
	if out, err := run("systemctl", "mask", "--now", apportUnit); err != nil {
		return fmt.Errorf("failed to stop and mask %s, which turns suid core dumps back on at boot: %w\n%s", apportUnit, err, out)
	}
	return nil
}

// ramHygieneLive are the kernel values ramHygieneSysctl sets that something
// else is known to change after it is applied: apport's start rewrites both.
var ramHygieneLive = []struct{ path, want string }{
	{"/proc/sys/fs/suid_dumpable", "0"},
	{"/proc/sys/kernel/core_pattern", discardCorePattern},
}

// verifyRAMHygiene fails unless the kernel reports the hardening in effect.
// The sysctl applying cleanly is not the proof: a writer that runs afterwards
// (apport did) changes the live value silently.
func verifyRAMHygiene(read func(string) ([]byte, error)) error {
	for _, v := range ramHygieneLive {
		b, err := read(v.path)
		if err != nil {
			return fmt.Errorf("failed to read %s after applying %s: %w", v.path, ramHygieneSysctlPath, err)
		}
		if got := strings.TrimSpace(string(b)); got != v.want {
			return fmt.Errorf("%s is %q after applying %s, want %q: another writer changed it (apport, /etc/sysctl.conf, a later sysctl.d file)", v.path, got, ramHygieneSysctlPath, v.want)
		}
	}
	return nil
}
