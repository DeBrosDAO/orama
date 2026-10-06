package wireguard

import "testing"

func TestPrivateKeyFromConfig(t *testing.T) {
	got, err := privateKeyFromConfig("[Interface]\nPrivateKey = abcDEF==\nAddress = 10.0.0.4/24\n")
	if err != nil {
		t.Fatalf("privateKeyFromConfig: %v", err)
	}
	if got != "abcDEF==" {
		t.Errorf("got %q", got)
	}

	if _, err := privateKeyFromConfig("[Interface]\nAddress = 10.0.0.4/24\n"); err == nil {
		t.Fatal("a config with no PrivateKey was accepted")
	}
	if _, err := privateKeyFromConfig("[Interface]\nPrivateKey =\n"); err == nil {
		t.Fatal("an empty PrivateKey was accepted")
	}
}
