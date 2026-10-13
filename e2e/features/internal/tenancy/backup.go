//go:build e2e_fleet

package tenancy

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
)

// Backup routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Namespace management"); shapes are
// core/pkg/gateway/handlers/backup and the frame format core/pkg/nsbackup.
// namespace-backup and namespace-backup-chaos share them.
const (
	PathBackup     = "/v1/namespace/backup"
	PathRestoreKey = "/v1/namespace/restore-key"
	PathRestore    = "/v1/namespace/restore"
	pathExec       = "/v1/rqlite/exec"
	pathCreate     = "/v1/rqlite/create-table"
	// SealedMagic starts every sealed backup (core/pkg/nsbackup/seal.go).
	SealedMagic = "ORBK"
	// backupKeyBytes is an X25519 key's length.
	backupKeyBytes = 32
)

// BackupKey is an owner's backup key pair; the private half never leaves the
// runner.
type BackupKey struct {
	priv, pub [backupKeyBytes]byte
	PubHex    string
}

// NewBackupKey is a fresh owner backup key pair.
func NewBackupKey(t testing.TB) BackupKey {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var out BackupKey
	copy(out.priv[:], k.Bytes())
	copy(out.pub[:], k.PublicKey().Bytes())
	out.PubHex = hex.EncodeToString(out.pub[:])
	return out
}

// PubKey is the public half, the key a restore's secrets are sealed to.
func (k BackupKey) PubKey() [backupKeyBytes]byte { return k.pub }

// PrivHex is the private half as `orama namespace backup-open --key` and
// the key file take it: 64 hex characters.
func (k BackupKey) PrivHex() string { return hex.EncodeToString(k.priv[:]) }

// File writes the private key as `orama namespace restore --key-file` reads
// it: 64 hex characters, 0600, in the test's own directory.
func (k BackupKey) File(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.key")
	if err := os.WriteFile(path, []byte(k.PrivHex()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Open decrypts and parses a sealed backup.
func (k BackupKey) Open(t testing.TB, sealed []byte) nsbackup.Payload {
	t.Helper()
	plain, err := nsbackup.Open(&k.priv, sealed)
	if err != nil {
		t.Fatalf("the backup does not open with the owner's key: %v", err)
	}
	p, err := nsbackup.UnmarshalPayload(plain)
	if err != nil {
		t.Fatalf("the opened backup does not parse: %v", err)
	}
	return p
}

// RestoreKey is GET /v1/namespace/restore-key as n's owner.
func RestoreKey(t testing.TB, n *ns.Namespace) [backupKeyBytes]byte {
	t.Helper()
	var out struct {
		Namespace string `json:"namespace"`
		PublicKey string `json:"public_key"`
	}
	if err := Get(t, n.Client, PathRestoreKey, Owner(n)).Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(out.PublicKey)
	if err != nil || len(raw) != backupKeyBytes || out.Namespace != n.Name {
		t.Fatalf("restore-key answered %+v", out)
	}
	var k [backupKeyBytes]byte
	copy(k[:], raw)
	return k
}

// BackupHTTP is POST /v1/namespace/backup as n's owner, sealed to k.
func BackupHTTP(t testing.TB, n *ns.Namespace, k BackupKey) []byte {
	t.Helper()
	r := Post(t, n.Client, PathBackup, Owner(n), map[string]string{"public_key": k.PubHex}).Expect(t, http.StatusOK)
	if len(r.Body) < len(SealedMagic) || string(r.Body[:len(SealedMagic)]) != SealedMagic {
		t.Fatalf("the backup is not a sealed %s file (%d bytes)", SealedMagic, len(r.Body))
	}
	return r.Body
}

// RestoreBody rewraps p's secrets to dest and encodes the restore request.
func RestoreBody(t testing.TB, p nsbackup.Payload, dest [backupKeyBytes]byte) []byte {
	t.Helper()
	req, err := p.Rewrap(&dest)
	if err != nil {
		t.Fatal(err)
	}
	body, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// RestoreHTTP posts a restore request as who.
func RestoreHTTP(t testing.TB, n *ns.Namespace, who Cred, body []byte) *gw.Response {
	t.Helper()
	return n.Client.MustSend(t, gw.Req{Method: http.MethodPost, Path: PathRestore, Bearer: who.Bearer, APIKey: who.APIKey,
		Header: http.Header{"Content-Type": {"application/octet-stream"}}, Body: body})
}

// NotesRows reads SELECT v FROM notes ORDER BY v as n's owner.
func NotesRows(t testing.TB, n *ns.Namespace) []string {
	t.Helper()
	var out struct {
		Items []struct {
			V string `json:"v"`
		} `json:"items"`
	}
	r := Post(t, n.Client, pathQuery, Owner(n), map[string]any{"sql": "SELECT v FROM notes ORDER BY v"})
	if err := r.Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	var vs []string
	for _, it := range out.Items {
		vs = append(vs, it.V)
	}
	return vs
}

// NotesInsert inserts v into the notes table as n's owner.
func NotesInsert(t testing.TB, n *ns.Namespace, v string) {
	t.Helper()
	Post(t, n.Client, pathExec, Owner(n), map[string]any{"sql": "INSERT INTO notes (v) VALUES (?)", "args": []any{v}}).Expect(t, http.StatusOK)
}

// NotesSeed creates the notes table with rows.
func NotesSeed(t testing.TB, n *ns.Namespace, vs ...string) {
	t.Helper()
	Post(t, n.Client, pathCreate, Owner(n), map[string]string{"schema": "CREATE TABLE notes (v TEXT)"}).Expect(t, http.StatusCreated)
	for _, v := range vs {
		NotesInsert(t, n, v)
	}
}
