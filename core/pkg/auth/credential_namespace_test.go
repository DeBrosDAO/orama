package auth

import "testing"

// Bugboard #284. AddCredential matched on wallet address alone, so one wallet could
// hold only a single credential per gateway. Authenticating to a second namespace
// silently REPLACED the first namespace's stored API key and refresh token — and the
// credential menu's "Add new wallet" option appeared to add one while destroying
// another. On devnet this ended with zero stored credentials after a login + delete:
// the login overwrote anchat-test's entry with anchat-v2's, then deleting anchat-v2
// removed the only row left.

const testGateway = "https://orama-devnet.network"

func TestAddCredential_keepsCredentialsForDifferentNamespaces(t *testing.T) {
	store := &EnhancedCredentialStore{}
	wallet := "0xB5d8A496C8b2412990d7D467E17727fdF5954afC"

	store.AddCredential(testGateway, &Credentials{Wallet: wallet, Namespace: "anchat-test", APIKey: "key-test"})
	store.AddCredential(testGateway, &Credentials{Wallet: wallet, Namespace: "anchat-v2", APIKey: "key-v2"})

	creds := store.Gateways[testGateway].Credentials
	if len(creds) != 2 {
		t.Fatalf("stored %d credentials, want 2 — the second namespace overwrote the first", len(creds))
	}

	byNS := map[string]string{}
	for _, c := range creds {
		byNS[c.Namespace] = c.APIKey
	}
	if byNS["anchat-test"] != "key-test" {
		t.Errorf("anchat-test key = %q, want key-test", byNS["anchat-test"])
	}
	if byNS["anchat-v2"] != "key-v2" {
		t.Errorf("anchat-v2 key = %q, want key-v2", byNS["anchat-v2"])
	}
}

// Re-authenticating to the SAME namespace must still refresh in place rather than
// accumulating duplicates.
func TestAddCredential_replacesSameWalletAndNamespace(t *testing.T) {
	store := &EnhancedCredentialStore{}
	wallet := "0xB5d8A496C8b2412990d7D467E17727fdF5954afC"

	store.AddCredential(testGateway, &Credentials{Wallet: wallet, Namespace: "anchat-v2", APIKey: "old"})
	store.AddCredential(testGateway, &Credentials{Wallet: wallet, Namespace: "anchat-v2", APIKey: "new"})

	creds := store.Gateways[testGateway].Credentials
	if len(creds) != 1 {
		t.Fatalf("stored %d credentials, want 1 (same wallet+namespace should refresh in place)", len(creds))
	}
	if creds[0].APIKey != "new" {
		t.Errorf("APIKey = %q, want the refreshed value", creds[0].APIKey)
	}
}

// Matching stays case-insensitive on the wallet, as before.
func TestAddCredential_walletMatchIsCaseInsensitive(t *testing.T) {
	store := &EnhancedCredentialStore{}

	store.AddCredential(testGateway, &Credentials{Wallet: "0xABC", Namespace: "ns1", APIKey: "old"})
	store.AddCredential(testGateway, &Credentials{Wallet: "0xabc", Namespace: "ns1", APIKey: "new"})

	creds := store.Gateways[testGateway].Credentials
	if len(creds) != 1 {
		t.Fatalf("stored %d credentials, want 1 — wallet comparison should be case-insensitive", len(creds))
	}
	if creds[0].APIKey != "new" {
		t.Errorf("APIKey = %q, want new", creds[0].APIKey)
	}
}

// Different wallets remain independent regardless of namespace.
func TestAddCredential_differentWalletsCoexist(t *testing.T) {
	store := &EnhancedCredentialStore{}

	store.AddCredential(testGateway, &Credentials{Wallet: "0xAAA", Namespace: "ns1", APIKey: "a"})
	store.AddCredential(testGateway, &Credentials{Wallet: "0xBBB", Namespace: "ns1", APIKey: "b"})

	if got := len(store.Gateways[testGateway].Credentials); got != 2 {
		t.Fatalf("stored %d credentials, want 2", got)
	}
}

// RemoveCredentialByNamespace must remove only the named namespace now that several
// can coexist for one wallet — this is the other half of the devnet data loss.
func TestRemoveCredentialByNamespace_leavesOtherNamespaceIntact(t *testing.T) {
	store := &EnhancedCredentialStore{}
	wallet := "0xB5d8A496C8b2412990d7D467E17727fdF5954afC"

	store.AddCredential(testGateway, &Credentials{Wallet: wallet, Namespace: "anchat-test", APIKey: "key-test"})
	store.AddCredential(testGateway, &Credentials{Wallet: wallet, Namespace: "anchat-v2", APIKey: "key-v2"})

	if !store.RemoveCredentialByNamespace(testGateway, "anchat-v2") {
		t.Fatal("RemoveCredentialByNamespace reported nothing removed")
	}

	creds := store.Gateways[testGateway].Credentials
	if len(creds) != 1 {
		t.Fatalf("stored %d credentials after removing one, want 1", len(creds))
	}
	if creds[0].Namespace != "anchat-test" {
		t.Errorf("surviving credential is %q, want anchat-test", creds[0].Namespace)
	}
}
