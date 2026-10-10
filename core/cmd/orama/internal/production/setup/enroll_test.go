package setup

import "testing"

func TestHostKeyInfos_namesTheTypeOfEachKey(t *testing.T) {
	hk := &hostKey{
		lines: []string{
			"203.0.113.5 ssh-ed25519 AAAAC3Nza",
			"203.0.113.5 ecdsa-sha2-nistp256 AAAAE2Vj",
			"203.0.113.5 ssh-rsa AAAAB3Nza",
			"broken",
		},
		fingerprints: []string{"SHA256:aaa", "SHA256:bbb", "SHA256:ccc", "SHA256:ddd"},
	}
	got := hostKeyInfos(hk)
	want := []HostKeyInfo{
		{"ed25519", "SHA256:aaa"}, {"ecdsa-sha2-nistp256", "SHA256:bbb"}, {"rsa", "SHA256:ccc"}, {"unknown", "SHA256:ddd"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEnrollPassword_aTypedPasswordBeatsTheVault(t *testing.T) {
	old := vaultPassword
	defer func() { vaultPassword = old }()
	vaultPassword = func(ip, user string) (string, error) {
		t.Errorf("the vault was asked for %s@%s though a password was typed", user, ip)
		return "from-vault", nil
	}
	got, err := enrollPassword(Options{IP: "203.0.113.5", User: "root", Password: "typed"})
	if err != nil || got != "typed" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestEnrollPassword_otherwiseTheVaultLogin(t *testing.T) {
	old := vaultPassword
	defer func() { vaultPassword = old }()
	vaultPassword = func(ip, user string) (string, error) { return user + "@" + ip, nil }
	got, err := enrollPassword(Options{IP: "203.0.113.5", User: "ubuntu"})
	if err != nil || got != "ubuntu@203.0.113.5" {
		t.Fatalf("got %q, %v", got, err)
	}
}
