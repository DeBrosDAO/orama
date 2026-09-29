package install

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// Isolated services (pkg/systemd isolatedServices) run as their own system
// account instead of the orama user, so a compromised one cannot read another
// Orama process's environment or files through /proc: ProtectProc=invisible
// hides the processes of other uids, and a different uid fails the kernel's
// ptrace check on /proc/<pid>/environ and /proc/<pid>/root. Install and
// upgrade create the accounts before anything is handed to them.

// serviceAccountShell is the login shell of every service account: none.
const serviceAccountShell = "/usr/sbin/nologin"

// supervisorUser is the account orama-node and every gateway run as. It
// writes the config files an isolated service reads.
const supervisorUser = "orama"

// commandRunner runs name with args and returns its combined output.
type commandRunner func(name string, args ...string) ([]byte, error)

// runCommand is the commandRunner install uses.
func runCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// serviceAccount is a system account, with a group of the same name, that an
// isolated service runs as.
type serviceAccount struct {
	name string
	// supervisorMember adds the orama user to the account's group, so the
	// supervisor can hand the service a file it writes.
	supervisorMember bool
}

// serviceAccountPlan is the account of every isolated service, in the order
// of isolatedServices.
func serviceAccountPlan() ([]serviceAccount, error) {
	var plan []serviceAccount
	for _, s := range systemd.IsolatedServices() {
		name, err := systemd.ServiceUser(s.Service, true)
		if err != nil {
			return nil, fmt.Errorf("the account of isolated service %s: %w", s.Service, err)
		}
		if name == supervisorUser {
			return nil, fmt.Errorf("isolated service %s maps to the shared %s account", s.Service, supervisorUser)
		}
		plan = append(plan, serviceAccount{name: name, supervisorMember: s.SupervisorInGroup})
	}
	return plan, nil
}

// EnsureServiceAccounts creates the account of every isolated service and the
// orama user's memberships in their groups, whatever of it is missing. It runs
// on every install and upgrade under the new binary, before Phase 4 hands the
// Corefile to the CoreDNS group and before Phase 5 restarts orama-node, which
// picks up its new groups only when it starts.
func (ps *ProductionSetup) EnsureServiceAccounts() error {
	if err := ensureServiceAccounts(runCommand); err != nil {
		return err
	}
	ps.logf("  ✓ Service accounts ensured")
	return nil
}

// ensureServiceAccounts applies serviceAccountPlan through run.
func ensureServiceAccounts(run commandRunner) error {
	plan, err := serviceAccountPlan()
	if err != nil {
		return err
	}
	for _, a := range plan {
		if err := ensureServiceAccount(run, a.name); err != nil {
			return err
		}
		if !a.supervisorMember {
			continue
		}
		if err := ensureSupervisorMembership(run, a.name); err != nil {
			return err
		}
	}
	return nil
}

// ensureServiceAccount creates the system account name, with its own group,
// no home directory and no login shell, unless `id` already knows it.
func ensureServiceAccount(run commandRunner, name string) error {
	if _, err := run("id", "-u", name); err == nil {
		return nil
	}
	out, err := run("useradd", "--system", "--user-group", "--no-create-home", "--shell", serviceAccountShell, name)
	if err != nil {
		return fmt.Errorf("create the %s service account: %w\n%s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureSupervisorMembership adds the orama user to group unless it is
// already a member.
func ensureSupervisorMembership(run commandRunner, group string) error {
	out, err := run("id", "-nG", supervisorUser)
	if err != nil {
		return fmt.Errorf("list the groups of the %s user: %w\n%s", supervisorUser, err, strings.TrimSpace(string(out)))
	}
	if slices.Contains(strings.Fields(string(out)), group) {
		return nil
	}
	out, err = run("usermod", "--append", "--groups", group, supervisorUser)
	if err != nil {
		return fmt.Errorf("add the %s user to the %s group: %w\n%s", supervisorUser, group, err, strings.TrimSpace(string(out)))
	}
	return nil
}
