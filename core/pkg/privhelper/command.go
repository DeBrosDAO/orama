package privhelper

import (
	"os"
	"os/exec"
)

// Command returns a command that runs tool with args as root: directly when
// this process already is root (the installer), otherwise through sudo and the
// helper. sudo runs with -n so a missing grant fails at once instead of waiting
// for a password nobody can type.
func Command(tool string, args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		return exec.Command(tool, args...)
	}
	return exec.Command("sudo", append([]string{"-n", Path, tool}, args...)...)
}

// SudoersRule is the /etc/sudoers.d drop-in granting user the helper. It names
// the binary with no arguments on purpose: the helper parses its own argv, and
// sudo-rs refuses the argument wildcards the old rules used.
func SudoersRule(user string) string {
	return user + " ALL=(root) NOPASSWD: " + Path + "\n"
}
