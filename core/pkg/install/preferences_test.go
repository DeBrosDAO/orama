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
