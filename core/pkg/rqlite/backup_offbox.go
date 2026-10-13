package rqlite

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"go.uber.org/zap"
)

// Off-box backup.
//
// A backup on the leader's own disk protects against nothing that actually
// happens: the disk fails, the VPS is deleted, `orama node wipe` removes it, or
// leadership moves and the series fragments across nodes so no single node
// holds a usable history. The registry is the cluster's memory of every
// namespace, node, DNS record and API key, and it had no restore path.
//
// Pushing the snapshot into IPFS makes it survive the machine that made it, and
// recording the CID makes it findable — an unfindable CID is the same as no
// backup at all.

// BackupUploader is the slice of the IPFS client this needs. Narrow so the
// backup path can be tested without a daemon.
type BackupUploader interface {
	Add(ctx context.Context, data io.Reader, name string) (string, error)
	Pin(ctx context.Context, cid string, name string, replicationFactor int) (*PinResult, error)
}

// PinResult is the outcome of a pin. Declared here rather than imported so this
// package does not depend on pkg/ipfs, which depends on this one.
type PinResult struct {
	Cid string
}

// SetBackupUploader supplies the store off-box backups are pushed to. Without
// one, backups stay local — which is the previous behaviour, and is reported.
func (r *RQLiteManager) SetBackupUploader(u BackupUploader, replicationFactor int) {
	r.backupUploader = u
	r.backupReplication = replicationFactor
}

// backupEncryptKey is the key snapshots are encrypted with before they leave
// the node. A registry snapshot holds every API key and DNS record in the
// fleet; pinning it unencrypted would publish that to anyone who can reach the
// cluster's IPFS.
func (r *RQLiteManager) backupEncryptKey() ([]byte, error) {
	if len(r.backupKey) == 0 {
		return nil, fmt.Errorf("no backup encryption key configured")
	}
	return r.backupKey, nil
}

// SetBackupEncryptionKey supplies the key snapshots are encrypted with.
func (r *RQLiteManager) SetBackupEncryptionKey(key []byte) { r.backupKey = key }

// pushBackupOffBox encrypts a snapshot, adds it to IPFS, pins it, and records
// where it went.
//
// Every failure is an Error, not a warning: a backup that did not leave the
// node is not a backup, and the whole point of this ticket is that nobody knew
// that was the case.
func (r *RQLiteManager) pushBackupOffBox(ctx context.Context, path string, db *sql.DB) error {
	if r.backupUploader == nil {
		return fmt.Errorf("no off-box backup store configured, so this snapshot exists only on this node's disk")
	}

	plaintext, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read snapshot %s: %w", path, err)
	}

	sum := sha256.Sum256(plaintext)
	digest := hex.EncodeToString(sum[:])

	key, err := r.backupEncryptKey()
	if err != nil {
		return fmt.Errorf("refusing to push an unencrypted registry snapshot off-box: %w", err)
	}
	sealed, err := sealBackup(plaintext, key)
	if err != nil {
		return fmt.Errorf("encrypt snapshot: %w", err)
	}

	name := fmt.Sprintf("rqlite-backup-%s", time.Now().UTC().Format(backupTimestampFormat))

	cid, err := r.backupUploader.Add(ctx, bytes.NewReader(sealed), name)
	if err != nil {
		return fmt.Errorf("add snapshot to IPFS: %w", err)
	}

	rf := r.backupReplication
	if rf <= 0 {
		rf = defaultBackupReplication
	}
	if _, err := r.backupUploader.Pin(ctx, cid, name, rf); err != nil {
		return fmt.Errorf("pin snapshot %s: %w", cid, err)
	}

	// Recorded LAST, and its failure is fatal to the operation: a pinned CID
	// nothing has written down cannot be found again.
	if err := recordBackup(ctx, db, cid, digest, len(plaintext), r.discoverConfig.RaftAdvAddress); err != nil {
		return fmt.Errorf("snapshot pinned as %s but not recorded, so it cannot be found again: %w", cid, err)
	}

	r.logger.Info("Registry backup pushed off-box",
		zap.String("cid", cid),
		zap.String("sha256", digest),
		zap.Int("size_bytes", len(plaintext)),
		zap.Int("replication_factor", rf))
	return nil
}

// defaultBackupReplication is how many peers hold each snapshot when the
// caller did not say.
const defaultBackupReplication = 3

// recordBackup writes the index row that makes a pinned snapshot findable.
func recordBackup(ctx context.Context, db *sql.DB, cid, digest string, size int, takenBy string) error {
	if db == nil {
		return fmt.Errorf("no database handle")
	}
	_, err := SafeExecContext(db, ctx, `
		INSERT INTO rqlite_backups (cid, sha256, size_bytes, taken_by, encrypted)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(cid) DO NOTHING`, cid, digest, size, takenBy)
	return err
}

// LatestBackup returns the most recent recorded backup.
type BackupRecord struct {
	TakenAt   string
	TakenBy   string
	CID       string
	SHA256    string
	SizeBytes int64
}

// LatestBackups returns the most recent recorded backups, newest first.
func LatestBackups(ctx context.Context, db *sql.DB, limit int) ([]BackupRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := SafeQueryContext(db, ctx, `
		SELECT taken_at, taken_by, cid, sha256, size_bytes
		  FROM rqlite_backups ORDER BY taken_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read backup index: %w", err)
	}
	defer rows.Close()

	var out []BackupRecord
	for rows.Next() {
		var rec BackupRecord
		if err := rows.Scan(&rec.TakenAt, &rec.TakenBy, &rec.CID, &rec.SHA256, &rec.SizeBytes); err != nil {
			return nil, fmt.Errorf("scan backup row: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// sealBackup encrypts a snapshot with AES-GCM.
//
// Written here rather than reusing pkg/secrets.Encrypt because that one takes
// and returns strings and base64-encodes the result — fine for a token, wasteful
// and lossy-looking for a multi-megabyte database file.
func sealBackup(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	// The nonce is prepended, so OpenBackup needs nothing but the key.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// OpenBackup decrypts a snapshot produced by sealBackup.
//
// Exported because restoring is the whole point: a backup nothing can open is
// not a backup.
func OpenBackup(sealed, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("snapshot is too short to be an encrypted backup (%d bytes)", len(sealed))
	}

	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt snapshot (wrong key, or the file is corrupt): %w", err)
	}
	return plaintext, nil
}

// VerifyBackup reports whether a decrypted snapshot matches its recorded digest.
func VerifyBackup(plaintext []byte, wantSHA256 string) error {
	sum := sha256.Sum256(plaintext)
	got := hex.EncodeToString(sum[:])
	if got != wantSHA256 {
		return fmt.Errorf("snapshot digest mismatch: got %s, recorded %s — the file is not the one that was backed up", got, wantSHA256)
	}
	return nil
}
