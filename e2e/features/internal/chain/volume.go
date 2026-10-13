//go:build e2e_fleet

package chain

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// SignStateFile is the validator's last-signed record, in the chain's data
// directory: a wiped data directory keeps it so the node can not sign a height
// it already signed.
const SignStateFile = "priv_validator_state.json"

// FS is the filesystem that holds the chain home, in bytes.
type FS struct {
	Mount string
	// Free is what root can write; Avail what every other user can.
	Free, Avail int64
}

// statFSScript prints `block size, free blocks, available blocks` of the
// chain home's filesystem and then its mount point.
func statFSScript() string {
	return fmt.Sprintf("stat -f -c '%%S %%f %%a' %[1]s && df --output=target %[1]s | tail -n 1", Home)
}

// ParseFS reads the output of statFSScript.
func ParseFS(stdout string) (FS, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		return FS{}, fmt.Errorf("want 2 lines (stat -f, mount point), got %q", stdout)
	}
	fields := strings.Fields(lines[0])
	if len(fields) != 3 {
		return FS{}, fmt.Errorf("want block size, free and available blocks, got %q", lines[0])
	}
	v := make([]int64, len(fields))
	for i, s := range fields {
		x, err := strconv.ParseInt(s, 10, 64)
		if err != nil || x < 0 {
			return FS{}, fmt.Errorf("stat -f field %q is not a count", s)
		}
		v[i] = x
	}
	mount := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(mount, "/") {
		return FS{}, fmt.Errorf("mount point %q is not a path", mount)
	}
	return FS{Mount: mount, Free: v[0] * v[1], Avail: v[0] * v[2]}, nil
}

// StatFS reads the filesystem of the chain home on n.
func (c *Chain) StatFS(t testing.TB, n fleet.Node) FS {
	t.Helper()
	out := c.Run(t, n, QueryBudget, statFSScript())
	if out.Exit != 0 {
		t.Fatalf("%s: cannot read the filesystem of %s (exit %d): %s", n.Name, Home, out.Exit, out.Stderr)
	}
	fs, err := ParseFS(out.Stdout)
	if err != nil {
		t.Fatalf("%s: the filesystem of %s: %v", n.Name, Home, err)
	}
	return fs
}

// WipeDataScript moves the data directory aside to backup and leaves a fresh
// one that holds only the validator's sign state. It touches backup+".wiped"
// before it moves anything and refuses to start when either exists, so a stale
// backup is never taken for this run's.
func WipeDataScript(backup string) string {
	data, marker := Home+"/data", backup+".wiped"
	owner := fmt.Sprintf("-o %[1]s -g %[1]s", ServiceUser)
	return fmt.Sprintf("set -e; test ! -e %[2]s; test ! -e %[3]s; touch %[3]s; mv %[1]s %[2]s; install -d %[4]s -m 0700 %[1]s; install %[4]s -m 0600 %[2]s/%[5]s %[1]s/%[5]s",
		fleet.ShellQuote(data), fleet.ShellQuote(backup), fleet.ShellQuote(marker), owner, SignStateFile)
}

// RestoreDataScript puts the old data directory back when WipeDataScript moved
// it and the test ended before removing it (the state sync did not finish): the
// unit is stopped, the newer sign state is carried into the old directory, and
// the old directory takes its place. It does nothing without the marker
// WipeDataScript wrote, so a backup it did not make is never swapped over live
// data.
func RestoreDataScript(backup string) string {
	data, marker := Home+"/data", backup+".wiped"
	return fmt.Sprintf("if [ -e %[3]s ] && [ -d %[2]s ]; then systemctl stop %[4]s; "+
		"if [ -e %[1]s/%[5]s ]; then install -o %[6]s -g %[6]s -m 0600 %[1]s/%[5]s %[2]s/%[5]s; fi; rm -rf %[1]s && mv %[2]s %[1]s; fi; rm -f %[3]s",
		fleet.ShellQuote(data), fleet.ShellQuote(backup), fleet.ShellQuote(marker), Unit, SignStateFile, ServiceUser)
}

// DropBackupScript removes the backup and its marker once the node has proved
// it needs neither.
func DropBackupScript(backup string) string {
	return "rm -rf " + fleet.ShellQuote(backup) + " " + fleet.ShellQuote(backup+".wiped")
}
