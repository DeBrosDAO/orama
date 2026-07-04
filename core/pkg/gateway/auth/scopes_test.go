package auth

import "testing"

func TestScopeSetHas(t *testing.T) {
	tests := []struct {
		name     string
		set      ScopeSet
		required string
		want     bool
	}{
		{"empty requirement always satisfied", ScopeSet{ScopeInvoke: {}}, "", true},
		{"admin satisfies anything", ScopeSet{ScopeAdmin: {}}, ScopeStorage, true},
		{"exact grant present", ScopeSet{ScopeStorage: {}}, ScopeStorage, true},
		{"grant absent", ScopeSet{ScopeInvoke: {}}, ScopeStorage, false},
		{"runtime cannot admin", ScopeSet{ScopeInvoke: {}, ScopeStorage: {}}, ScopeAdmin, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.set.Has(tt.required); got != tt.want {
				t.Errorf("Has(%q) = %v, want %v", tt.required, got, tt.want)
			}
		})
	}
}

func TestScopesFromStoredGrandfather(t *testing.T) {
	// NULL/empty stored scopes => admin (legacy key grandfather).
	for _, raw := range []string{"", "   "} {
		set := ScopesFromStored(raw)
		if !set.IsAdmin() {
			t.Errorf("ScopesFromStored(%q) should grandfather to admin, got %v", raw, set)
		}
	}
	// A real scope list must NOT grandfather to admin.
	set := ScopesFromStored("invoke,storage")
	if set.IsAdmin() {
		t.Errorf("ScopesFromStored(\"invoke,storage\") must not be admin")
	}
	if !set.Has(ScopeInvoke) || !set.Has(ScopeStorage) || set.Has(ScopePush) {
		t.Errorf("parsed set wrong: %v", set)
	}
}

func TestParseScopesEmptyIsNotAdmin(t *testing.T) {
	// ParseScopes is literal — empty yields an empty set (NOT admin). Only
	// ScopesFromStored applies the grandfather.
	if ParseScopes("").IsAdmin() {
		t.Error("ParseScopes(\"\") must be empty, not admin")
	}
}

func TestDataPlaneScopesNeverAdmin(t *testing.T) {
	dp := DataPlaneScopes()
	if dp.IsAdmin() {
		t.Fatal("data-plane scopes must never include admin")
	}
	for _, g := range []string{ScopeInvoke, ScopeStorage, ScopePush, ScopeWebRTC, ScopeProxy} {
		if !dp.Has(g) {
			t.Errorf("data-plane set missing %q", g)
		}
	}
	if dp.Has(ScopeAdmin) {
		t.Error("data-plane must not satisfy admin")
	}
}

func TestProfileGrants(t *testing.T) {
	tests := []struct {
		profile string
		wantOK  bool
		admin   bool
		hasStor bool
	}{
		{"admin", true, true, true},
		{"app-runtime", true, false, true},
		{"invoke-only", true, false, false},
		{"nonsense", false, false, false},
	}
	for _, tt := range tests {
		grants, ok := ProfileGrants(tt.profile)
		if ok != tt.wantOK {
			t.Errorf("ProfileGrants(%q) ok=%v, want %v", tt.profile, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		set := ScopeSet(setOf(grants))
		if set.IsAdmin() != tt.admin {
			t.Errorf("%q admin=%v, want %v", tt.profile, set.IsAdmin(), tt.admin)
		}
		if set.Has(ScopeStorage) != tt.hasStor {
			t.Errorf("%q hasStorage=%v, want %v", tt.profile, set.Has(ScopeStorage), tt.hasStor)
		}
	}
}

func TestNormalizeGrants(t *testing.T) {
	// Profile expands + canonicalizes (sorted).
	got, err := NormalizeGrants("app-runtime")
	if err != nil {
		t.Fatalf("app-runtime: %v", err)
	}
	if got != "invoke,proxy,push,storage,webrtc" {
		t.Errorf("app-runtime canonical = %q", got)
	}
	// Explicit list, comma and space tolerant + deduped + sorted.
	got, err = NormalizeGrants("storage, invoke invoke")
	if err != nil {
		t.Fatalf("explicit list: %v", err)
	}
	if got != "invoke,storage" {
		t.Errorf("explicit canonical = %q", got)
	}
	// Unknown grant is a loud error, not a silent mint.
	if _, err := NormalizeGrants("invoke,bogus"); err == nil {
		t.Error("expected error for unknown grant")
	}
	// Empty is rejected.
	if _, err := NormalizeGrants("   "); err == nil {
		t.Error("expected error for empty scope")
	}
}

func TestCanonicalStable(t *testing.T) {
	a := ScopeSet{ScopeStorage: {}, ScopeInvoke: {}, ScopePush: {}}.Canonical()
	b := ScopeSet{ScopePush: {}, ScopeInvoke: {}, ScopeStorage: {}}.Canonical()
	if a != b {
		t.Errorf("Canonical not stable: %q vs %q", a, b)
	}
	if a != "invoke,push,storage" {
		t.Errorf("Canonical = %q", a)
	}
}
