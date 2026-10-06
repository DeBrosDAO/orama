package secrets

import "testing"

func TestCheckSuccessor(t *testing.T) {
	cur := Root{CurrentID: "3", CurrentIKM: "gen-3", PreviousID: "2", PreviousIKM: "gen-2"}
	for _, tc := range []struct {
		name    string
		current Root
		next    Root
		wantErr bool
	}{
		{"newer generation", cur, Root{CurrentID: "4", CurrentIKM: "gen-4", PreviousID: "3", PreviousIKM: "gen-3"}, false},
		{"older generation is a rollback", cur, Root{CurrentID: "2", CurrentIKM: "gen-2"}, true},
		{"same generation, same key (format rewrite or retry)", cur, Root{CurrentID: "3", CurrentIKM: "gen-3", WriteVersioned: true}, false},
		{"same generation, previous forgotten", cur, Root{CurrentID: "3", CurrentIKM: "gen-3"}, false},
		{"same generation, different key", cur, Root{CurrentID: "3", CurrentIKM: "attacker"}, true},
		{"no id on the pushed root", cur, Root{CurrentIKM: "gen-4"}, true},
		{"a gateway with no root yet takes any", Root{}, Root{CurrentID: "1", CurrentIKM: "gen-1"}, false},
		{"a root that predates generations is generation 1", Root{CurrentID: FirstID, CurrentIKM: "cluster-secret"}, Root{CurrentID: "2", CurrentIKM: "x"}, false},
		{"unusable own root is an error, not a pass", Root{CurrentID: "abc", CurrentIKM: "k"}, Root{CurrentID: "4", CurrentIKM: "x"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckSuccessor(tc.current, tc.next); (err != nil) != tc.wantErr {
				t.Fatalf("CheckSuccessor err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestRootGeneration(t *testing.T) {
	if n, err := (Root{CurrentID: " 7 "}).Generation(); err != nil || n != 7 {
		t.Fatalf("got %d, %v", n, err)
	}
	for _, id := range []string{"", "0", "-1", "x"} {
		if _, err := (Root{CurrentID: id}).Generation(); err == nil {
			t.Errorf("id %q parsed as a generation", id)
		}
	}
}
