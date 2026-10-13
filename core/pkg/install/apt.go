package install

import (
	"os/exec"
	"strconv"
)

const (
	aptGetPath = "/usr/bin/apt-get"
	aptPath    = "/usr/sbin:/usr/bin:/sbin:/bin"
	// aptLockTimeoutSeconds is how long apt-get waits for a dpkg lock another
	// apt holds before it fails. A fresh cloud image runs unattended-upgrades
	// on first boot, which holds the lock for minutes; without the wait the
	// install of a brand-new node failed at once with "Could not get lock".
	aptLockTimeoutSeconds = 900
)

// aptCommand is apt-get from its fixed path, non-interactive, waiting for a
// dpkg lock another apt holds instead of failing at once.
func aptCommand(args ...string) *exec.Cmd {
	lock := "DPkg::Lock::Timeout=" + strconv.Itoa(aptLockTimeoutSeconds)
	cmd := exec.Command(aptGetPath, append([]string{"-o", lock}, args...)...)
	cmd.Env = []string{"DEBIAN_FRONTEND=noninteractive", "PATH=" + aptPath}
	return cmd
}
