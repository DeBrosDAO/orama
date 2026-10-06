package installers

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// commandRunner runs a command and returns its combined output. The Tor
// installer and the legacy Anyone cleaner take one so their command sequences
// can be tested without a system to run them on.
type commandRunner func(name string, args ...string) (string, error)

func execRunner(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// aptLockTimeoutSeconds lets apt-get wait for the dpkg lock held by
// unattended-upgrades instead of failing at once.
const aptLockTimeoutSeconds = 300

// aptGet runs apt-get with the dpkg lock timeout.
func aptGet(run commandRunner, args ...string) error {
	lock := "DPkg::Lock::Timeout=" + strconv.Itoa(aptLockTimeoutSeconds)
	return runChecked(run, "apt-get", append([]string{"-o", lock}, args...)...)
}

// runChecked runs a command and folds its output into the error when it fails.
func runChecked(run commandRunner, name string, args ...string) error {
	if out, err := run(name, args...); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return nil
}
