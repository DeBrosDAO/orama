//go:build e2e_fleet

package infra

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// RebootBudget bounds a server's reboot until SSH answers again.
	RebootBudget = 10 * time.Minute
	bootIDPath   = "/proc/sys/kernel/random/boot_id"
	sshAttempt   = 30 * time.Second
	// rebootCmd schedules the reboot two seconds out, so the SSH command that
	// asks for it returns before the machine goes down.
	rebootCmd = "systemd-run --on-active=2 --timer-property=AccuracySec=1 /bin/systemctl reboot"
)

// BootID is the kernel's id of the current boot of n.
func BootID(t testing.TB, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	return strings.TrimSpace(string(f.ReadFile(t, n, bootIDPath)))
}

// Reboot reboots n the way a provider or an operator does (a clean systemd
// reboot, no CLI involved) and waits until it answers SSH on a new boot. What
// comes back up is the product's business; the caller asserts it.
func Reboot(t testing.TB, f *fleet.Fleet, n fleet.Node) {
	t.Helper()
	before := BootID(t, f, n)
	f.MustExec(t, n, rebootCmd)
	eventually.Require(t, PollEvery, RebootBudget, n.Name+" to come back on a new boot", func() (bool, error) {
		ctx, cancel := context.WithTimeout(t.Context(), sshAttempt)
		defer cancel()
		out, err := f.SSHFor(t, n).Run(ctx, "cat "+bootIDPath)
		if err != nil {
			return false, err
		}
		if id := strings.TrimSpace(out.Stdout); id == before || id == "" {
			return false, fmt.Errorf("%s is still on boot %s", n.Name, before)
		}
		return true, nil
	})
}
