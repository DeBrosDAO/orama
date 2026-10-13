package auth

import "testing"

// Signing in again to a namespace that already has a credential replaces it
// in its own slot. The index returned is that slot, so the login makes it the
// default instead of whichever credential happens to be last (found on
// stagenet: a fresh stagenetbeta login left anchatdemo as the default).
func TestAddCredential_returnsTheSlotItWrote(t *testing.T) {
	store := &EnhancedCredentialStore{}
	gw := "https://gw.example"
	a := store.AddCredential(gw, &Credentials{Wallet: "0xA", Namespace: "beta"})
	b := store.AddCredential(gw, &Credentials{Wallet: "0xA", Namespace: "anchatdemo"})
	if a != 0 || b != 1 {
		t.Fatalf("new credentials got slots %d and %d", a, b)
	}
	again := store.AddCredential(gw, &Credentials{Wallet: "0xa", Namespace: "beta", APIKey: "fresh"})
	if again != 0 {
		t.Fatalf("re-login to beta returned slot %d, want 0", again)
	}
	if !store.SetDefaultCredential(gw, again) {
		t.Fatal("could not set the default")
	}
	got := store.GetDefaultCredential(gw)
	if got.Namespace != "beta" || got.APIKey != "fresh" {
		t.Fatalf("default is %q, want the fresh beta credential", got.Namespace)
	}
	if n := len(store.Gateways[gw].Credentials); n != 2 {
		t.Fatalf("re-login added a credential: %d stored", n)
	}
}
