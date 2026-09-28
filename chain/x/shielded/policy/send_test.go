package policy

import "testing"

func TestBlockUserToUser(t *testing.T) {
	cases := []struct {
		name       string
		fromModule bool
		toModule   bool
		denom      string
		wantErr    bool
	}{
		{name: "user to user norama", denom: BaseDenom, wantErr: true},
		{name: "user to module norama", toModule: true, denom: BaseDenom},
		{name: "module to user norama", fromModule: true, denom: BaseDenom},
		{name: "module to module norama", fromModule: true, toModule: true, denom: BaseDenom},
		{name: "user token", denom: "factory/orama1abc/usd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := BlockUserToUser(tc.fromModule, tc.toModule, tc.denom)
			if tc.wantErr && err == nil {
				t.Fatal("expected ErrPublicPayment")
			}
			if !tc.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}
