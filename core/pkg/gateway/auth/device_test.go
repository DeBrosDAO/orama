package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// These run the real SQL against the real schema. What is being tested is the
// predicates — approved, denied, claimed, expired — which are the whole of what
// makes a device login safe: a fake that models them is not a test of them.

func TestDeviceFlow_endToEnd(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	pending, err := s.StartDeviceAuthorization(ctx, "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if pending.DeviceCode == "" || pending.UserCode == "" {
		t.Fatal("a login with no codes")
	}
	if pending.Interval != int(DevicePollInterval/time.Second) {
		t.Errorf("interval = %d", pending.Interval)
	}

	// Nobody has approved it, so the first poll says so.
	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceAuthorizationPending) {
		t.Fatalf("first poll = %v, want authorization_pending", err)
	}

	if err := s.ApproveDeviceAuthorization(ctx, pending.UserCode, "0xowner", "anchat"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	claimed, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Subject != "0xowner" || claimed.Namespace != "anchat" {
		t.Errorf("claimed %+v", claimed)
	}

	// A device code collects a session once. A code left in a shell history or
	// a log collects nothing.
	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceCodeUnknown) {
		t.Errorf("second claim = %v, want invalid_grant", err)
	}
}

// The waiting machine asked for one namespace; being handed a session in
// another means it does whatever it was going to do in the wrong tenant.
func TestApproveDeviceAuthorization_refusesADifferentNamespace(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	pending, err := s.StartDeviceAuthorization(ctx, "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	err = s.ApproveDeviceAuthorization(ctx, pending.UserCode, "0xowner", "somebody-else")
	if err == nil {
		t.Fatal("a login for one namespace was approved into another")
	}
	if !strings.Contains(err.Error(), "anchat") {
		t.Errorf("the refusal does not name the namespace that was asked for: %v", err)
	}

	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceAuthorizationPending) {
		t.Errorf("the refused approval changed the login's state: %v", err)
	}
}

// A login that names no namespace takes whichever one the approver signs in to,
// which is the case from a machine that has never been logged in.
func TestApproveDeviceAuthorization_takesTheApproversNamespaceWhenNoneWasAsked(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	pending, err := s.StartDeviceAuthorization(ctx, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.ApproveDeviceAuthorization(ctx, pending.UserCode, "0xowner", "anchat"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	claimed, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Namespace != "anchat" {
		t.Errorf("namespace = %q, want the approver's", claimed.Namespace)
	}
}

func TestDeviceFlow_aDeniedLoginStopsRatherThanPolling(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	pending, err := s.StartDeviceAuthorization(ctx, "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.DenyDeviceAuthorization(ctx, pending.UserCode); err != nil {
		t.Fatalf("deny: %v", err)
	}

	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceAccessDenied) {
		t.Errorf("poll after a refusal = %v, want access_denied", err)
	}
	// And it cannot be approved afterwards: a refusal is final, or refusing is
	// only a suggestion.
	if err := s.ApproveDeviceAuthorization(ctx, pending.UserCode, "0xowner", "anchat"); err == nil {
		t.Error("a refused login was approved")
	}
}

func TestDeviceFlow_anExpiredLoginIsNotApprovableOrClaimable(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	pending, err := s.StartDeviceAuthorization(ctx, "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(sqliteTime)
	if _, err := db.db.Exec(`UPDATE device_authorizations SET expires_at = ?`, past); err != nil {
		t.Fatalf("expire: %v", err)
	}

	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceCodeExpired) {
		t.Errorf("poll = %v, want expired_token", err)
	}
	if err := s.ApproveDeviceAuthorization(ctx, pending.UserCode, "0xowner", "anchat"); !errors.Is(err, ErrDeviceCodeExpired) {
		t.Errorf("approve = %v, want expired_token", err)
	}
}

// A deadline the driver hands back as a zero time — which is what go-sqlite3
// does with a value it cannot parse — is past, not absent.
func TestDeviceFlow_aDeadlineTheDriverCannotParseIsPast(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	pending, err := s.StartDeviceAuthorization(ctx, "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := db.db.Exec(`UPDATE device_authorizations SET expires_at = 'whenever'`); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceCodeExpired) {
		t.Errorf("poll = %v, want expired_token", err)
	}
}

// The interval is enforced rather than advised, or a client in a tight loop is
// the login endpoint's load.
func TestClaimDeviceAuthorization_refusesAPollInsideTheInterval(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	pending, err := s.StartDeviceAuthorization(ctx, "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceAuthorizationPending) {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceSlowDown) {
		t.Errorf("immediate second poll = %v, want slow_down", err)
	}
}

// Being told to slow down about a login that is already over would leave a
// client polling out its ten minutes on a refusal — and being told to slow
// down about one that has just been approved would delay the session by an
// interval for no reason.
func TestClaimDeviceAuthorization_terminalStatesBeatTheInterval(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	pending, err := s.StartDeviceAuthorization(ctx, "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceAuthorizationPending) {
		t.Fatalf("first poll: %v", err)
	}
	if err := s.DenyDeviceAuthorization(ctx, pending.UserCode); err != nil {
		t.Fatalf("deny: %v", err)
	}

	if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceAccessDenied) {
		t.Errorf("poll = %v, want access_denied even inside the interval", err)
	}

	t.Run("and an approval is handed over inside the interval too", func(t *testing.T) {
		pending, err := s.StartDeviceAuthorization(ctx, "anchat")
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); !errors.Is(err, ErrDeviceAuthorizationPending) {
			t.Fatalf("first poll: %v", err)
		}
		if err := s.ApproveDeviceAuthorization(ctx, pending.UserCode, "0xowner", "anchat"); err != nil {
			t.Fatalf("approve: %v", err)
		}
		if _, err := s.ClaimDeviceAuthorization(ctx, pending.DeviceCode); err != nil {
			t.Errorf("poll right after approval = %v, want the session", err)
		}
	})
}

// The device code is the credential the waiting machine holds, so a table read
// must not hand out something that collects a session.
func TestStartDeviceAuthorization_storesNoDeviceCodeInTheClear(t *testing.T) {
	s, db, _ := realRegistry(t)

	pending, err := s.StartDeviceAuthorization(context.Background(), "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	var stored string
	if err := db.db.QueryRow(`SELECT device_code FROM device_authorizations`).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored == pending.DeviceCode {
		t.Fatal("the device code is stored as it was handed out")
	}
	if len(stored) != 64 {
		t.Errorf("stored device code is %q, want a sha256 hex digest", stored)
	}
}

// Nothing accumulates: a login nobody came back for, or one already used, is
// gone by the next login.
func TestStartDeviceAuthorization_sweepsWhatNobodyCameBackFor(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	stale, err := s.StartDeviceAuthorization(ctx, "anchat")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(sqliteTime)
	if _, err := db.db.Exec(`UPDATE device_authorizations SET expires_at = ?`, past); err != nil {
		t.Fatalf("expire: %v", err)
	}

	if _, err := s.StartDeviceAuthorization(ctx, "anchat"); err != nil {
		t.Fatalf("second start: %v", err)
	}

	var rows int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM device_authorizations`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d rows remain, want only the live one", rows)
	}
	if _, err := s.ClaimDeviceAuthorization(ctx, stale.DeviceCode); !errors.Is(err, ErrDeviceCodeUnknown) {
		t.Errorf("the swept login still answers: %v", err)
	}
}

func TestNormalizeUserCode(t *testing.T) {
	code, err := newUserCode()
	if err != nil {
		t.Fatalf("newUserCode: %v", err)
	}
	if len(code) != deviceUserCodeLength+1 || !strings.Contains(code, "-") {
		t.Fatalf("user code %q is not grouped for reading aloud", code)
	}

	// However somebody types it back.
	for _, typed := range []string{code, strings.ToLower(code), strings.ReplaceAll(code, "-", ""), " " + code + " "} {
		got, err := NormalizeUserCode(typed)
		if err != nil {
			t.Fatalf("NormalizeUserCode(%q): %v", typed, err)
		}
		if got != code {
			t.Errorf("NormalizeUserCode(%q) = %q, want %q", typed, got, code)
		}
	}

	for _, bad := range []string{"", "ABC", "ABCD-EFGHI"} {
		if _, err := NormalizeUserCode(bad); err == nil {
			t.Errorf("NormalizeUserCode(%q) was accepted", bad)
		}
	}
}

// A user code an attacker can guess is one they can have approved by asking the
// user to approve "their own" login.
func TestNewUserCode_isDrawnFreshEveryTime(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := newUserCode()
		if err != nil {
			t.Fatalf("newUserCode: %v", err)
		}
		if seen[code] {
			t.Fatalf("%q came up twice in 200 draws", code)
		}
		seen[code] = true
		for _, c := range strings.ReplaceAll(code, "-", "") {
			if !strings.ContainsRune(deviceUserCodeAlphabet, c) {
				t.Fatalf("%q contains %q, which is not in the alphabet", code, c)
			}
		}
	}
}

// The string path, which is the one production takes: rqlite decodes JSON, so
// every column arrives as text and an expires_at nobody can parse is a deadline
// that never arrives. go-sqlite3 parses the column for us, so no test that goes
// through SQLite reaches this.
func TestDeviceExpired_readsWhatEitherClientReturns(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	for _, tc := range []struct {
		name string
		cell any
		want bool
	}{
		{"a NULL deadline is past, because the column is NOT NULL", nil, true},
		{"a time.Time in the future is not", future, false},
		{"a time.Time in the past is", past, true},
		{"a string in the future is not", future.UTC().Format(sqliteTime), false},
		{"a string in the past is", past.UTC().Format(sqliteTime), true},
		{"RFC3339 is read too", future.UTC().Format(time.RFC3339), false},
		{"a string nobody can parse is past", "whenever", true},
		{"an empty string is past", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deviceExpired(tc.cell); got != tc.want {
				t.Errorf("deviceExpired(%v) = %v, want %v", tc.cell, got, tc.want)
			}
		})
	}
}

// Two machines polling one device code, or two people approving one user code,
// both pass the read that precedes the write: the read cannot see a write that
// has not happened. The compare-and-swap is what makes exactly one of them win,
// and it is the only thing that does.
func TestDeviceFlow_theWritesAreCompareAndSwap(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	t.Run("one approval wins", func(t *testing.T) {
		pending, err := s.StartDeviceAuthorization(ctx, "anchat")
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		code, err := NormalizeUserCode(pending.UserCode)
		if err != nil {
			t.Fatal(err)
		}

		first, err := s.recordApproval(ctx, code, "0xowner", "anchat")
		if err != nil || !first {
			t.Fatalf("first approval: won=%v err=%v", first, err)
		}
		second, err := s.recordApproval(ctx, code, "0xsomebodyelse", "anchat")
		if err != nil {
			t.Fatalf("second approval: %v", err)
		}
		if second {
			t.Error("a second approval of the same code also won, so who approved it is whoever wrote last")
		}
	})

	t.Run("one collection wins", func(t *testing.T) {
		pending, err := s.StartDeviceAuthorization(ctx, "anchat")
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := s.ApproveDeviceAuthorization(ctx, pending.UserCode, "0xowner", "anchat"); err != nil {
			t.Fatalf("approve: %v", err)
		}
		hashed := sha256Hex(pending.DeviceCode)

		first, err := s.claimApproval(ctx, hashed)
		if err != nil || !first {
			t.Fatalf("first collection: won=%v err=%v", first, err)
		}
		second, err := s.claimApproval(ctx, hashed)
		if err != nil {
			t.Fatalf("second collection: %v", err)
		}
		if second {
			t.Error("one approval was collected twice, so two machines hold a session from one login")
		}
	})

	t.Run("a refused login cannot then be approved", func(t *testing.T) {
		pending, err := s.StartDeviceAuthorization(ctx, "anchat")
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		code, err := NormalizeUserCode(pending.UserCode)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DenyDeviceAuthorization(ctx, code); err != nil {
			t.Fatalf("deny: %v", err)
		}

		won, err := s.recordApproval(ctx, code, "0xowner", "anchat")
		if err != nil {
			t.Fatalf("approval after refusal: %v", err)
		}
		if won {
			t.Error("a refused login was approved anyway, so refusing is only a suggestion")
		}
	})
}
