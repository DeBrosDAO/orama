package globalcmd

import "testing"

func TestCheckChainClientUsers(t *testing.T) {
	if err := checkChainClientUsers(nil, false); err != nil {
		t.Errorf("no users: %v", err)
	}
	if err := checkChainClientUsers([]string{"debian"}, true); err != nil {
		t.Errorf("a named user with --colocated: %v", err)
	}
	for name, users := range map[string][]string{"empty": {""}, "blank": {"  "}, "one empty among others": {"debian", ""}} {
		if err := checkChainClientUsers(users, true); err == nil {
			t.Errorf("%s: an empty --chain-client-user was accepted", name)
		}
	}
	if err := checkChainClientUsers([]string{"debian"}, false); err == nil {
		t.Error("--chain-client-user without --colocated was accepted")
	}
}
