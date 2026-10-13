package install

import "testing"

func TestPreferences_roleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := SavePreferences(dir, &NodePreferences{Branch: "main", Role: "global"}); err != nil {
		t.Fatal(err)
	}
	got := LoadPreferences(dir)
	if got.Role != "global" {
		t.Fatalf("role %q, want global", got.Role)
	}
	if got.Branch != "main" {
		t.Fatalf("branch %q", got.Branch)
	}
}

func TestPreferencesForInstall_keepsTheGlobalLayersRole(t *testing.T) {
	dir := t.TempDir()
	if err := SavePreferences(dir, &NodePreferences{Branch: "main", Role: "both", GlobalNetns: "orama-global"}); err != nil {
		t.Fatal(err)
	}
	got := PreferencesForInstall(dir, true)
	if got.Role != "both" || got.GlobalNetns != "orama-global" {
		t.Fatalf("role %q, global_netns %q: a re-install must keep what the global layer recorded", got.Role, got.GlobalNetns)
	}
	if !got.Nameserver || got.Branch != "main" {
		t.Fatalf("nameserver %v, branch %q: the install's own choices must apply", got.Nameserver, got.Branch)
	}
}

func TestPreferencesForInstall_freshMachine(t *testing.T) {
	got := PreferencesForInstall(t.TempDir(), false)
	if got.Role != "" || got.GlobalNetns != "" || got.Nameserver || got.Branch != "main" {
		t.Fatalf("a machine with no preferences gets only the defaults, got %+v", got)
	}
}

func TestPreferencesForInstall_nameserverFlagOverridesTheStoredOne(t *testing.T) {
	dir := t.TempDir()
	if err := SavePreferences(dir, &NodePreferences{Branch: "main", Nameserver: true, Role: "global"}); err != nil {
		t.Fatal(err)
	}
	got := PreferencesForInstall(dir, false)
	if got.Nameserver || got.Role != "global" {
		t.Fatalf("got %+v: the nameserver choice is the install's, the role is the machine's", got)
	}
}

func TestPreferencesForInstall_savedPreferencesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := SavePreferences(dir, &NodePreferences{Role: "both", GlobalNetns: "orama-global"}); err != nil {
		t.Fatal(err)
	}
	if err := SavePreferences(dir, PreferencesForInstall(dir, false)); err != nil {
		t.Fatal(err)
	}
	if got := LoadPreferences(dir); got.Role != "both" || got.GlobalNetns != "orama-global" {
		t.Fatalf("after a re-install the file holds %+v", got)
	}
}
