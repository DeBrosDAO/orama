package install

import (
	"errors"
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
// on every install and upgrade under the new binary, first thing when the
// namespace templates are installed (InstallNamespaceTemplates): before the
// units that name the accounts are loaded, before the Corefile is handed to
// the CoreDNS group, and before Phase 5 restarts orama-node, which picks up
// its new groups only when it starts.
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

// getentNotFound is getent's exit status for a key the database does not
// hold. Any other failure is an error, never "missing".
const getentNotFound = 2

// exitCoder is an error that carries a process exit status (*exec.ExitError).
type exitCoder interface {
	ExitCode() int
}

// accountEntryExists asks getent whether database (passwd or group) holds
// name.
func accountEntryExists(run commandRunner, database, name string) (bool, error) {
	out, err := run("getent", database, name)
	if err == nil {
		return true, nil
	}
	var code exitCoder
	if errors.As(err, &code) && code.ExitCode() == getentNotFound {
		return false, nil
	}
	return false, fmt.Errorf("look up %s in the %s database: %w\n%s", name, database, err, strings.TrimSpace(string(out)))
}

// ensureServiceAccount creates the system account name, with a group of the
// same name, no home directory and no login shell, unless it exists. A group
// of that name left without its user (a user deleted by hand, or a useradd
// interrupted between the two) is used as the account's group: --user-group
// refuses to create a group that exists.
func ensureServiceAccount(run commandRunner, name string) error {
	userExists, err := accountEntryExists(run, "passwd", name)
	if err != nil || userExists {
		return err
	}
	groupExists, err := accountEntryExists(run, "group", name)
	if err != nil {
		return err
	}
	groupFlag := []string{"--user-group"}
	if groupExists {
		groupFlag = []string{"-g", name}
	}
	args := append([]string{"--system"}, groupFlag...)
	args = append(args, "--no-create-home", "--shell", serviceAccountShell, name)
	out, err := run("useradd", args...)
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
