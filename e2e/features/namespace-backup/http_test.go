//go:build e2e_fleet

package namespacebackup

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
)

const (
	pathExec = "/v1/rqlite/exec"
	// hugeUploadBudget bounds sending the 256 MiB over-limit restore.
	hugeUploadBudget = 10 * time.Minute
	// bulkRows rows of bulkRowBytes zero bytes, hex-encoded (twice the size),
	// make the database several MB, so a backup takes long enough to overlap.
	bulkRows     = 8
	bulkRowBytes = 512 << 10
)

// TestBackup_roundTripRestoresDataAndLeavesKeys: a backup holds the namespace's
// database; restoring it puts the rows back as they were and removes what was
// written after, and running the same restore again is safe. API keys are not
// in that database (they are in the cluster registry), so a restore neither
// brings back a revoked one nor removes one minted since the backup
// (docs/CLI_REFERENCE.md "orama namespace restore"; handlers/backup).
func TestBackup_roundTripRestoresDataAndLeavesKeys(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tenancy.NotesSeed(t, n, "alpha", "beta", "γάμμα \u202e")
	before := tenancy.APIKey(t, n, "cache")
	k := tenancy.NewBackupKey(t)
	p := k.Open(t, tenancy.BackupHTTP(t, n, k))
	if p.Namespace != n.Name || len(p.RQLite) == 0 {
		t.Fatalf("the backup is of %q with a %d-byte database", p.Namespace, len(p.RQLite))
	}
	tenancy.NotesInsert(t, n, "written-after")
	after := tenancy.APIKey(t, n, "cache")
	body := tenancy.RestoreBody(t, p, tenancy.RestoreKey(t, n))
	for range 2 { // re-running a restore is safe (the handler's own message)
		var res struct {
			Namespace   string `json:"namespace"`
			RQLiteBytes int    `json:"rqlite_bytes"`
			Secrets     int    `json:"secrets"`
		}
		if err := tenancy.RestoreHTTP(t, n, tenancy.Owner(n), body).Expect(t, http.StatusOK).Decode(&res); err != nil {
			t.Fatal(err)
		}
		if res.Namespace != n.Name || res.RQLiteBytes != len(p.RQLite) || res.Secrets != len(p.Secrets) {
			t.Fatalf("restore answered %+v for a backup of %d bytes and %d secrets", res, len(p.RQLite), len(p.Secrets))
		}
		if got := tenancy.NotesRows(t, n); !slices.Equal(got, []string{"alpha", "beta", "γάμμα \u202e"}) {
			t.Fatalf("after restore the rows are %v", got)
		}
	}
	for name, key := range map[string]string{"before the backup": before, "minted after the backup": after} {
		if r := tenancy.Get(t, n.Client, "/v1/cache/health", tenancy.Cred{APIKey: key}); r.Status != http.StatusOK {
			t.Errorf("the key %s answered %d after the restore: a restore does not touch keys", name, r.Status)
		}
	}
}

// TestBackup_refusedBeforeAnyWrite: a restore sealed to the wrong key, a
// corrupt request, a backup of another namespace and a truncated body are all
// refused, and the database is untouched.
func TestBackup_refusedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	pair := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := pair[0], pair[1]
	tenancy.NotesSeed(t, a, "keep")
	tenancy.NotesSeed(t, b, "other")
	k := tenancy.NewBackupKey(t)
	pa, pb := k.Open(t, tenancy.BackupHTTP(t, a, k)), k.Open(t, tenancy.BackupHTTP(t, b, k))
	tenancy.NotesInsert(t, a, "live")
	good := tenancy.RestoreBody(t, pa, tenancy.RestoreKey(t, a))
	wrongDest := tenancy.RestoreBody(t, pa, tenancy.NewBackupKey(t).PubKey())
	corrupt := bytes.Clone(good)
	corrupt[len(corrupt)/2] ^= 0xff
	cases := map[string]struct {
		body   []byte
		status []int
	}{
		"secrets sealed to another key":  {wrongDest, []int{http.StatusBadRequest}},
		"corrupt":                        {corrupt, []int{http.StatusBadRequest}},
		"truncated":                      {good[:len(good)/3], []int{http.StatusBadRequest}},
		"another namespace's backup":     {tenancy.RestoreBody(t, pb, tenancy.RestoreKey(t, a)), []int{http.StatusConflict}},
		"empty":                          {[]byte{}, []int{http.StatusBadRequest}},
		"a sealed backup, not a request": {tenancy.BackupHTTP(t, a, k), []int{http.StatusBadRequest}},
	}
	if len(pa.Secrets) == 0 {
		delete(cases, "secrets sealed to another key") // nothing sealed to refuse
	}
	for name, c := range cases {
		if r := tenancy.RestoreHTTP(t, a, tenancy.Owner(a), c.body); !slices.Contains(c.status, r.Status) {
			t.Errorf("%s: want %v, got %d %.200s", name, c.status, r.Status, r.Body)
		}
		if got := tenancy.NotesRows(t, a); !slices.Equal(got, []string{"keep", "live"}) {
			t.Fatalf("%s: a refused restore changed the database to %v", name, got)
		}
	}
}

// TestBackup_ownerOnly: backup and restore are the owner's; an admin grant is
// not enough, and another namespace's credential is refused
// (handlers/backup/handler.go authorize).
func TestBackup_ownerOnly(t *testing.T) {
	t.Parallel()
	pair := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := pair[0], pair[1]
	k := tenancy.NewBackupKey(t)
	body := map[string]string{"public_key": k.PubHex}
	for who, cred := range map[string]tenancy.Cred{
		"admin member":   {Bearer: tenancy.Member(t, a, tenancy.RoleAdmin).Token()},
		"runtime member": {Bearer: tenancy.Member(t, a, tenancy.RoleRuntime).Token()},
		"admin key":      {APIKey: tenancy.APIKey(t, a, "admin")},
		"B's key":        {APIKey: tenancy.APIKey(t, b, "admin")},
	} {
		if r := tenancy.Post(t, a.Client, tenancy.PathBackup, cred, body); r.Status != http.StatusForbidden {
			t.Errorf("%s backing A up: want 403, got %d", who, r.Status)
		}
		if r := tenancy.RestoreHTTP(t, a, cred, []byte("x")); r.Status != http.StatusForbidden {
			t.Errorf("%s restoring A: want 403, got %d", who, r.Status)
		}
	}
	tenancy.ExpectRefused(t, tenancy.Post(t, a.Client, tenancy.PathBackup, tenancy.Cred{}, body), http.StatusUnauthorized, tenancy.CodeMissing)
	tenancy.ExpectDenied(t, tenancy.Post(t, a.Client, tenancy.PathBackup, tenancy.Owner(b), body), "B's owner backing up A")
}

func TestBackup_malformedRequests(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for name, body := range map[string]any{
		"no key": map[string]string{}, "short key": map[string]string{"public_key": "abcd"},
		"not hex": map[string]string{"public_key": string(bytes.Repeat([]byte("z"), 64))}, "not json": []byte("public_key=x"),
	} {
		if r := tenancy.Post(t, n.Client, tenancy.PathBackup, tenancy.Owner(n), body); r.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, r.Status)
		}
	}
	tenancy.Get(t, n.Client, tenancy.PathBackup, tenancy.Owner(n)).Expect(t, http.StatusMethodNotAllowed)
	tenancy.Get(t, n.Client, tenancy.PathRestore, tenancy.Owner(n)).Expect(t, http.StatusMethodNotAllowed)
	tenancy.Post(t, n.Client, tenancy.PathRestoreKey, tenancy.Owner(n), nil).Expect(t, http.StatusMethodNotAllowed)
}

// TestBackup_restoreSizeCap: a restore request over the frame limit (a
// 256 MiB database plus the 8 MiB header allowance, handlers/backup
// MaxRestoreBytes) is refused with 413 and nothing is written. The body is one
// byte over, so a smaller one would be a legal-sized frame that is read in full
// and refused as malformed (400).
func TestBackup_restoreSizeCap(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tenancy.NotesSeed(t, n, "intact")
	huge := make([]byte, backuphandlers.MaxRestoreBytes+1)
	// Send, not MustSend: 256 MiB does not go up in the client's default
	// per-request budget (gw.RequestBudget).
	ctx, cancel := context.WithTimeout(t.Context(), hugeUploadBudget)
	defer cancel()
	r, err := n.Client.For(t).Send(ctx, gw.Req{Method: http.MethodPost, Path: tenancy.PathRestore, Bearer: n.Owner.Token(),
		Header: http.Header{"Content-Type": {"application/octet-stream"}}, Body: huge})
	if err != nil {
		t.Fatalf("the over-limit restore could not be sent: %v", err)
	}
	if r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an over-limit restore answered %d, want 413", r.Status)
	}
	if got := tenancy.NotesRows(t, n); !slices.Equal(got, []string{"intact"}) {
		t.Fatalf("an over-limit restore changed the database to %v", got)
	}
}

// TestBackup_oneAtATime: concurrent backups on one gateway either succeed or
// are refused 429 with Retry-After, and with a database of several MB they
// overlap, so at least one is refused; none is corrupt (handlers/backup
// begin: one backup or restore at a time per gateway).
func TestBackup_oneAtATime(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	tenancy.NotesSeed(t, n, "x")
	// Generated in the database, deterministically (zeroblob, not randomblob:
	// every replica must apply the same rows).
	tenancy.Post(t, n.Client, pathExec, tenancy.Owner(n), map[string]any{"sql": fmt.Sprintf(
		"INSERT INTO notes (v) WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < %d) SELECT hex(zeroblob(%d)) FROM c",
		bulkRows, bulkRowBytes)}).Expect(t, http.StatusOK)
	c := n.Client.PinTo(f.State.Nodes[0].PublicIP)
	k := tenancy.NewBackupKey(t)
	const racers = 5
	var wg sync.WaitGroup
	results := make([][]byte, racers)
	statuses := make([]int, racers)
	retry := make([]string, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.JSON(t.Context(), http.MethodPost, tenancy.PathBackup, n.Owner.Token(), map[string]string{"public_key": k.PubHex}, nil)
			if r != nil {
				statuses[i], results[i], retry[i] = r.Status, r.Body, r.Header.Get("Retry-After")
			} else {
				t.Errorf("racer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	refused := 0
	for i, st := range statuses {
		switch st {
		case http.StatusOK:
			k.Open(t, results[i])
		case http.StatusTooManyRequests:
			refused++
			if retry[i] == "" {
				t.Errorf("racer %d: 429 without Retry-After", i)
			}
		default:
			t.Errorf("racer %d: want 200 or 429, got %d", i, st)
		}
	}
	if refused == 0 {
		t.Errorf("%d overlapping backups of a %d MiB database on one gateway were all served: the one-at-a-time guard did not refuse any", racers, bulkRows*bulkRowBytes*2>>20)
	}
}
