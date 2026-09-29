package install

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// fakeAccounts is a node's accounts as `id`, `useradd` and `usermod` see them.
type fakeAccounts struct {
	users        map[string]bool
	oramaGroups  []string
	failCommand  string // a command that fails
	calls        [][]string
	groupListErr bool
}

func (f *fakeAccounts) run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if name == f.failCommand {
		return []byte(name + ": refused"), errors.New("exit status 1")
	}
	switch {
	case name == "id" && len(args) == 2 && args[0] == "-u":
		if f.users[args[1]] {
			return []byte("998\n"), nil
		}
		return []byte("id: no such user"), errors.New("exit status 1")
	case name == "id" && len(args) == 2 && args[0] == "-nG":
		if f.groupListErr {
			return []byte("id: cannot read"), errors.New("exit status 1")
		}
		return []byte(strings.Join(f.oramaGroups, " ") + "\n"), nil
	case name == "useradd":
		f.users[args[len(args)-1]] = true
		return nil, nil
	case name == "usermod":
		f.oramaGroups = append(f.oramaGroups, args[2])
		return nil, nil
	}
	return nil, errors.New("unexpected command " + name)
}

// changes are the calls that change the node: useradd and usermod.
func (f *fakeAccounts) changes() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if c[0] == "useradd" || c[0] == "usermod" {
			out = append(out, c)
		}
	}
	return out
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{users: map[string]bool{"orama": true}, oramaGroups: []string{"orama"}}
}

func TestEnsureServiceAccounts_createsEachAccountAndTheSFUMembership(t *testing.T) {
	f := newFakeAccounts()
	if err := ensureServiceAccounts(f.run); err != nil {
		t.Fatalf("ensureServiceAccounts: %v", err)
	}
	want := [][]string{
		{"useradd", "--system", "--user-group", "--no-create-home", "--shell", "/usr/sbin/nologin", "orama-coredns"},
		{"useradd", "--system", "--user-group", "--no-create-home", "--shell", "/usr/sbin/nologin", "orama-sfu"},
		{"usermod", "--append", "--groups", "orama-sfu", "orama"},
	}
	if got := f.changes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes:\n got  %q\n want %q", got, want)
	}
}

// Every upgrade runs it again. A node that already has everything is left
// exactly as it is.
func TestEnsureServiceAccounts_isIdempotent(t *testing.T) {
	f := newFakeAccounts()
	if err := ensureServiceAccounts(f.run); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := ensureServiceAccounts(f.run); err != nil {
		t.Fatal(err)
	}
	if got := f.changes(); len(got) != 0 {
		t.Fatalf("a second run changed the node: %q", got)
	}
}

// Membership is by exact group name: orama in orama-sfu2 is not orama in
// orama-sfu.
func TestEnsureServiceAccounts_similarGroupNameIsNotMembership(t *testing.T) {
	f := newFakeAccounts()
	f.users["orama-coredns"], f.users["orama-sfu"] = true, true
	f.oramaGroups = []string{"orama", "orama-sfu2", "xorama-sfu"}
	if err := ensureServiceAccounts(f.run); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"usermod", "--append", "--groups", "orama-sfu", "orama"}}
	if got := f.changes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes %q, want %q", got, want)
	}
}

func TestEnsureServiceAccounts_useraddFailureNamesTheAccount(t *testing.T) {
	f := newFakeAccounts()
	f.failCommand = "useradd"
	err := ensureServiceAccounts(f.run)
	if err == nil || !strings.Contains(err.Error(), "orama-coredns") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v, want one naming orama-coredns with useradd's output", err)
	}
}

func TestEnsureServiceAccounts_usermodFailureIsAnError(t *testing.T) {
	f := newFakeAccounts()
	f.failCommand = "usermod"
	err := ensureServiceAccounts(f.run)
	if err == nil || !strings.Contains(err.Error(), "orama-sfu group") {
		t.Fatalf("err = %v, want one naming the orama-sfu group", err)
	}
}

// Not knowing the orama user's groups is an error, not "not a member".
func TestEnsureServiceAccounts_unreadableGroupsIsAnError(t *testing.T) {
	f := newFakeAccounts()
	f.groupListErr = true
	if err := ensureServiceAccounts(f.run); err == nil {
		t.Fatal("an unreadable group list was treated as a result")
	}
	for _, c := range f.changes() {
		if c[0] == "usermod" {
			t.Fatalf("usermod ran without knowing the current groups: %q", c)
		}
	}
}

func TestServiceAccountPlan_oneDistinctAccountPerIsolatedService(t *testing.T) {
	plan, err := serviceAccountPlan()
	if err != nil {
		t.Fatal(err)
	}
	isolated := systemd.IsolatedServices()
	if len(plan) != len(isolated) {
		t.Fatalf("plan has %d accounts for %d isolated services", len(plan), len(isolated))
	}
	seen := map[string]bool{}
	for i, a := range plan {
		if a.name == supervisorUser || a.name == "" {
			t.Errorf("isolated service %s gets account %q", isolated[i].Service, a.name)
		}
		if seen[a.name] {
			t.Errorf("account %s is planned twice", a.name)
		}
		seen[a.name] = true
		if a.supervisorMember != isolated[i].SupervisorInGroup {
			t.Errorf("%s: supervisor membership %v, want %v", a.name, a.supervisorMember, isolated[i].SupervisorInGroup)
		}
	}
}
