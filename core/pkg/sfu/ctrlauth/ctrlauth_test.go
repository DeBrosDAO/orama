package ctrlauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Unix(1_700_000_000, 0)

const testTarget = "10.0.0.1:8443"

func testKey(t *testing.T, secret string) []byte {
	t.Helper()
	key, err := Key(secret)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func goodTicket() Ticket {
	return Ticket{Namespace: "ns", Room: "r1", UserID: "0xabc", DeviceID: "dev", IssuedAtMs: testNow.UnixMilli(), Expires: testNow.Add(TicketTTL).Unix()}
}

func TestKey_emptySecret(t *testing.T) {
	if _, err := Key("  \n"); err == nil {
		t.Fatal("an empty TURN secret derived a key")
	}
}

func TestKey_trailingNewlineDerivesTheSameKey(t *testing.T) {
	a, b := testKey(t, "secret"), testKey(t, "secret\n")
	if string(a) != string(b) {
		t.Fatal("a trailing newline changed the key")
	}
}

func TestTicket_roundTrip(t *testing.T) {
	key := testKey(t, "s")
	want := goodTicket()
	want.Muted = true
	want.EventSink = "http://10.0.0.5:10004"
	token, err := want.Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenTicket(key, token, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ticket = %+v, want %+v", got, want)
	}
}

func TestOpenTicket_refusals(t *testing.T) {
	key := testKey(t, "s")
	token, _ := goodTicket().Seal(key)
	expired := goodTicket()
	expired.Expires = testNow.Add(-time.Second).Unix()
	expiredToken, _ := expired.Seal(key)
	payload, sig, _ := strings.Cut(token, ".")

	cases := []struct {
		name  string
		token string
		key   []byte
		now   time.Time
		want  error
	}{
		{"missing", "", key, testNow, ErrNoTicket},
		{"no separator", payload, key, testNow, ErrBadTicket},
		{"signature of another key", token, testKey(t, "other"), testNow, ErrBadTicket},
		{"tampered payload", "x" + payload + "." + sig, key, testNow, ErrBadTicket},
		{"non-hex signature", payload + ".zz", key, testNow, ErrBadTicket},
		{"expired", expiredToken, key, testNow, ErrExpiredTicket},
		{"expired at the boundary", token, key, testNow.Add(TicketTTL), ErrExpiredTicket},
		{"oversized", strings.Repeat("a", maxTicketBytes+1), key, testNow, ErrBadTicket},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := OpenTicket(c.key, c.token, c.now); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestTicketSeal_refusesIncompleteAndPublicSink(t *testing.T) {
	key := testKey(t, "s")
	if _, err := (Ticket{Namespace: "ns", Room: "r"}).Seal(key); err == nil {
		t.Error("a ticket with no user and no expiry was sealed")
	}
	public := goodTicket()
	public.EventSink = "http://203.0.113.9:80"
	if _, err := public.Seal(key); err == nil {
		t.Error("a ticket naming a public event sink was sealed")
	}
}

func TestValidateSink(t *testing.T) {
	for sink, ok := range map[string]bool{
		"":                       true,
		"http://10.0.0.7:10004":  true,
		"https://10.0.0.7:10004": false,
		"http://10.0.0.7:1/x":    false,
		"http://example.com:80":  false,
		"http://127.0.0.1:80":    false,
		"not a url":              false,
	} {
		if err := ValidateSink(sink); (err == nil) != ok {
			t.Errorf("ValidateSink(%q) = %v, want ok=%v", sink, err, ok)
		}
	}
}

func TestRequestMAC(t *testing.T) {
	key := testKey(t, "s")
	body := []byte(`{"room":"r","user":"u"}`)
	h := Sign(key, testTarget, "POST", "/admin/kick", body, testNow)

	if err := Verify(key, testTarget, h, "post", "/admin/kick", body, testNow); err != nil {
		t.Fatalf("a valid stamp was refused: %v", err)
	}
	cases := []struct {
		name   string
		header string
		method string
		path   string
		body   []byte
		now    time.Time
	}{
		{"another body", h, "POST", "/admin/kick", []byte(`{"room":"r","user":"v"}`), testNow},
		{"another path", h, "POST", "/admin/mute", body, testNow},
		{"another method", h, "GET", "/admin/kick", body, testNow},
		{"stale", h, "POST", "/admin/kick", body, testNow.Add(2 * time.Minute)},
		{"from the future", h, "POST", "/admin/kick", body, testNow.Add(-2 * time.Minute)},
		{"missing", "", "POST", "/admin/kick", body, testNow},
		{"garbage", "1.zz", "POST", "/admin/kick", body, testNow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Verify(key, testTarget, c.header, c.method, c.path, c.body, c.now); !errors.Is(err, ErrBadMAC) {
				t.Fatalf("err = %v, want ErrBadMAC", err)
			}
		})
	}
	if err := Verify(testKey(t, "other"), testTarget, h, "POST", "/admin/kick", body, testNow); !errors.Is(err, ErrBadMAC) {
		t.Fatal("a stamp verified under another namespace's key")
	}
}

func TestRequestMAC_aStampForOneTargetIsRefusedByAnother(t *testing.T) {
	key := testKey(t, "s")
	body := []byte(`{"room":"r","user":"u"}`)
	h := Sign(key, "10.0.0.1:8443", "POST", "/admin/kick", body, testNow)

	if err := Verify(key, "10.0.0.1:8443", h, "POST", "/admin/kick", body, testNow); err != nil {
		t.Fatalf("a stamp was refused by the target it was made for: %v", err)
	}
	for _, other := range []string{"10.0.0.2:8443", "10.0.0.1:8444", "http://10.0.0.1:6001", ""} {
		if err := Verify(key, other, h, "POST", "/admin/kick", body, testNow); !errors.Is(err, ErrBadMAC) {
			t.Errorf("a stamp for 10.0.0.1:8443 verified at %q: err = %v", other, err)
		}
	}
}

func TestRequestMAC_everyStampIsUnique(t *testing.T) {
	key := testKey(t, "s")
	body := []byte(`{"room":"r"}`)
	a, b := Sign(key, testTarget, "POST", "/admin/mute", body, testNow), Sign(key, testTarget, "POST", "/admin/mute", body, testNow)
	if a == b {
		t.Fatal("two stamps of the same request in the same second are identical, so one honest repeat would look like a replay")
	}
	for _, h := range []string{a, b} {
		if err := Verify(key, testTarget, h, "POST", "/admin/mute", body, testNow); err != nil {
			t.Fatalf("a fresh stamp was refused: %v", err)
		}
	}
}

func TestReplayGuard_secondUseIsRefused(t *testing.T) {
	key := testKey(t, "s")
	g := NewReplayGuard(16)
	h := Sign(key, testTarget, "POST", "/admin/kick", nil, testNow)

	if err := g.Use(h, testNow); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := g.Use(h, testNow.Add(time.Second)); !errors.Is(err, ErrReplayed) {
		t.Fatalf("second use: err = %v, want ErrReplayed", err)
	}
	if err := g.Use(Sign(key, testTarget, "POST", "/admin/kick", nil, testNow), testNow); err != nil {
		t.Fatalf("another stamp of the same request was refused: %v", err)
	}
	if err := g.Use("garbage", testNow); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("a malformed stamp: err = %v, want ErrBadMAC", err)
	}
}

func TestReplayGuard_forgetsStampsThatCannotVerifyAnyMore(t *testing.T) {
	key := testKey(t, "s")
	g := NewReplayGuard(16)
	h := Sign(key, testTarget, "POST", "/admin/kick", nil, testNow)
	_ = g.Use(h, testNow)

	later := testNow.Add(replayWindow + time.Second)
	if err := g.Use(Sign(key, testTarget, "POST", "/admin/kick", nil, later), later); err != nil {
		t.Fatal(err)
	}
	if len(g.seen) != 1 {
		t.Fatalf("the guard holds %d stamps, want only the live one", len(g.seen))
	}
}

func TestReplayGuard_isBounded(t *testing.T) {
	key := testKey(t, "s")
	g := NewReplayGuard(4)
	for i := 0; i < 50; i++ {
		if err := g.Use(Sign(key, testTarget, "POST", "/admin/kick", nil, testNow), testNow); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.seen) != 4 || g.order.Len() != 4 {
		t.Fatalf("the guard holds %d stamps, want its capacity of 4", len(g.seen))
	}
}

func TestTicket_admissionGenerationRoundTripsAndAnOldTicketReadsAsZero(t *testing.T) {
	key, err := Key("test-secret-key-32bytes-long!!!!")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	base := Ticket{Namespace: "ns", Room: "r", UserID: "u", IssuedAtMs: now.UnixMilli(), Expires: now.Add(TicketTTL).Unix()}

	withGen := base
	withGen.AdmitGen = 7
	token, err := withGen.Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := OpenTicket(key, token, now); err != nil || got.AdmitGen != 7 {
		t.Fatalf("OpenTicket = %+v, %v, want generation 7", got, err)
	}

	// A ticket from a gateway that predates generations has no such field.
	token, _ = base.Seal(key)
	if got, err := OpenTicket(key, token, now); err != nil || got.AdmitGen != 0 {
		t.Fatalf("OpenTicket = %+v, %v, want generation 0", got, err)
	}
}
