package capability

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var now = time.Unix(1_800_000_000, 0)

func authority(t *testing.T) *Authority {
	t.Helper()
	a, err := NewAuthority("cluster-secret-for-tests")
	if err != nil {
		t.Fatalf("authority: %v", err)
	}
	return a
}

func mint(t *testing.T, a *Authority) (string, *Claims) {
	t.Helper()
	token, claims, err := a.Mint("anchat", "rpc-router", "mailbox-7", "device-1", time.Hour, now)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return token, claims
}

func TestVerify_acceptsWhatItMinted(t *testing.T) {
	a := authority(t)
	token, minted := mint(t, a)
	got, err := a.Verify(token, "anchat", "rpc-router", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("a genuine capability was refused: %v", err)
	}
	if *got != *minted || got.Resource != "mailbox-7" || got.IssuerDevice != "device-1" {
		t.Errorf("verified %+v, minted %+v", got, minted)
	}
}

func TestVerify_refusals(t *testing.T) {
	a := authority(t)
	token, _ := mint(t, a)
	other, _ := NewAuthority("another-cluster")
	foreign, _, _ := other.Mint("anchat", "rpc-router", "mailbox-7", "device-1", time.Hour, now)
	payload, sig, _ := strings.Cut(token, ".")

	for name, tc := range map[string]struct {
		token, ns, fn string
		at            time.Time
	}{
		"expired":               {token, "anchat", "rpc-router", now.Add(time.Hour)},
		"another function":      {token, "anchat", "other-fn", now},
		"another namespace":     {token, "elsewhere", "rpc-router", now},
		"another cluster's":     {foreign, "anchat", "rpc-router", now},
		"a tampered payload":    {"eyJ2IjoxfQ." + sig, "anchat", "rpc-router", now},
		"a truncated signature": {payload + "." + sig[:10], "anchat", "rpc-router", now},
		"no signature":          {payload, "anchat", "rpc-router", now},
		"empty":                 {"", "anchat", "rpc-router", now},
		"oversized":             {strings.Repeat("a", maxTokenLength+1), "anchat", "rpc-router", now},
	} {
		if _, err := a.Verify(tc.token, tc.ns, tc.fn, tc.at); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestMint_refusals(t *testing.T) {
	a := authority(t)
	for name, err := range map[string]error{
		"no issuing device": func() error { _, _, e := a.Mint("anchat", "fn", "r", "", time.Hour, now); return e }(),
		"too long":          func() error { _, _, e := a.Mint("anchat", "fn", "r", "d", MaxTTL+time.Second, now); return e }(),
		"too short":         func() error { _, _, e := a.Mint("anchat", "fn", "r", "d", time.Second, now); return e }(),
		"a huge resource": func() error {
			_, _, e := a.Mint("anchat", "fn", strings.Repeat("r", MaxResourceLength+1), "d", time.Hour, now)
			return e
		}(),
		"no function": func() error { _, _, e := a.Mint("anchat", "", "r", "d", time.Hour, now); return e }(),
	} {
		if err == nil {
			t.Errorf("%s: minted", name)
		}
	}
	if _, _, err := a.Mint("anchat", "fn", "r", "", time.Hour, now); !errors.Is(err, ErrNoIssuer) {
		t.Errorf("a capability with no issuing device: %v", err)
	}
	if _, err := NewAuthority(""); err == nil {
		t.Error("an authority with no cluster secret was built")
	}
}

// Each mint is its own capability, revocable on its own.
func TestMint_eachCapabilityHasItsOwnID(t *testing.T) {
	a := authority(t)
	_, first := mint(t, a)
	_, second := mint(t, a)
	if first.ID == second.ID {
		t.Error("two capabilities share an id; revoking one would revoke the other")
	}
}

// The revocation list and the socket sweeper see a capability as a token with
// no subject, revoked by its own id or by its issuing device.
func TestRevocationClaims(t *testing.T) {
	_, c := mint(t, authority(t))
	rc := c.RevocationClaims()
	if rc.Sub != "" || rc.Did != "device-1" || rc.Jti != RevocationID("anchat", c.ID) || rc.Exp != c.ExpiresAt {
		t.Errorf("revocation claims %+v", rc)
	}
}
