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

// suidDumpablePath is the kernel's live fs.suid_dumpable.
const suidDumpablePath = "/proc/sys/fs/suid_dumpable"

// verifySuidDumpable fails unless the kernel reports suid core dumps off. The
// sysctl applying cleanly is not the proof: a writer that runs afterwards
// (apport did) changes the live value silently.
func verifySuidDumpable(read func(string) ([]byte, error)) error {
	b, err := read(suidDumpablePath)
	if err != nil {
		return fmt.Errorf("failed to read %s after applying %s: %w", suidDumpablePath, ramHygieneSysctlPath, err)
	}
	if v := strings.TrimSpace(string(b)); v != "0" {
		return fmt.Errorf("fs.suid_dumpable is %s after applying %s, want 0: another writer re-enabled suid core dumps (apport, /etc/sysctl.conf, a later sysctl.d file)", v, ramHygieneSysctlPath)
	}
	return nil
}
