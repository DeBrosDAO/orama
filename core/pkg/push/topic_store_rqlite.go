package push

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// TopicSecretsKeyPurpose is the HKDF purpose for topic token encryption. It is
// separate from SecretsKeyPurpose so a push_devices ciphertext copied into
// push_topics does not decrypt.
const TopicSecretsKeyPurpose = "push-topic-tokens"

// TopicFingerprintPurpose is the HKDF purpose for the topic token fingerprint.
// Separate from TokenFingerprintPurpose so push_topics.token_fp and
// push_devices.token_fp cannot be joined to link a topic to an account.
const TopicFingerprintPurpose = "push-topic-token-fp"

// RqliteTopicStore is a PushTopicStore backed by the namespace's RQLite, with
// the provider token AES-256-GCM encrypted at rest (as RqliteDeviceStore).
//
// It takes no logger: nothing it could log about a topic is worth the risk of
// putting a topic id next to a caller in a log line. Every failure is returned.
type RqliteTopicStore struct {
	db     rqlite.Client
	encKey []byte
	fpKey  []byte
	holder *secrets.Holder
	now    func() time.Time
}

// NewRqliteTopicStore derives the topic token keys.
//
// encIKM is the encryption root the token is sealed under; a secrets rotate
// re-encrypts it (secrets.NamespaceColumns). fingerprintSecret keys the token
// fingerprint and must NOT be the rotating encryption root: the walk that
// re-encrypts tokens cannot recompute fingerprints, and a fingerprint that
// changed with the key would stop matching, so a rotated topic would no
// longer replace the device's previous one. The gateway passes the cluster
// secret, which a secrets rotate leaves alone.
func NewRqliteTopicStore(db rqlite.Client, encIKM, fingerprintSecret string) (*RqliteTopicStore, error) {
	if fingerprintSecret == "" {
		return nil, fmt.Errorf("push topic store: a fingerprint secret (the cluster secret) is required")
	}
	encKey, err := secrets.DeriveKey(encIKM, TopicSecretsKeyPurpose)
	if err != nil {
		return nil, fmt.Errorf("derive push-topic key: %w", err)
	}
	fpKey, err := secrets.DeriveKey(fingerprintSecret, TopicFingerprintPurpose)
	if err != nil {
		return nil, fmt.Errorf("derive push-topic fingerprint key: %w", err)
	}
	return &RqliteTopicStore{db: db, encKey: encKey, fpKey: fpKey, now: time.Now}, nil
}

// SetHolder lets a secrets rotate take effect without restarting this process.
func (s *RqliteTopicStore) SetHolder(h *secrets.Holder) {
	if s != nil {
		s.holder = h
	}
}

// topicRow is the scan target for Get.
type topicRow struct {
	Provider       string
	TokenEncrypted string
	ExpiresAt      int64
}

// tokenFingerprint is a keyed HMAC of the token, bound to the namespace so
// the same token fingerprints differently in every namespace and tables from
// two namespaces cannot be joined on it.
func (s *RqliteTopicStore) tokenFingerprint(namespace, token string) string {
	mac := hmac.New(sha256.New, s.fpKey)
	mac.Write([]byte(namespace))
	mac.Write([]byte{0})
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// expiryFrom is now plus TopicTTL, rounded up to TopicExpiryGranularity.
func expiryFrom(now time.Time) int64 {
	exp := now.Add(TopicTTL)
	if rounded := exp.Truncate(TopicExpiryGranularity); rounded.Before(exp) {
		exp = rounded.Add(TopicExpiryGranularity)
	}
	return exp.Unix()
}

// Register implements PushTopicStore.
//
// One atomic batch: first remove every OTHER topic in the namespace carrying
// the same provider token (a rotated topic replaces the device's previous one)
// and every expired topic (the table only grows when a device registers, which
// is when this runs), then upsert this topic. Either both happen or neither.
func (s *RqliteTopicStore) Register(ctx context.Context, t PushTopic) (int64, error) {
	if t.Namespace == "" || t.Provider == "" {
		return 0, fmt.Errorf("push topic: namespace and provider required")
	}
	if err := ValidateTopicID(t.TopicID); err != nil {
		return 0, err
	}
	if t.Token == "" {
		return 0, ErrEmptyToken
	}
	encToken, err := secrets.Seal(s.holder, TopicSecretsKeyPurpose, s.encKey, t.Token)
	if err != nil {
		return 0, fmt.Errorf("push topic: encrypt token: %w", err)
	}
	tokenFP := s.tokenFingerprint(t.Namespace, t.Token)
	now := s.now()
	expiresAt := expiryFrom(now)

	res, err := s.db.Batch(ctx, []rqlite.BatchOp{{
		Kind: rqlite.BatchOpExec,
		SQL: `DELETE FROM push_topics
		       WHERE namespace = ? AND topic_id != ? AND (token_fp = ? OR expires_at <= ?)`,
		Args: []interface{}{t.Namespace, t.TopicID, tokenFP, now.Unix()},
	}, {
		Kind: rqlite.BatchOpExec,
		SQL: `INSERT INTO push_topics
			(namespace, topic_id, provider, token_encrypted, token_fp, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(namespace, topic_id) DO UPDATE SET
			provider = excluded.provider,
			token_encrypted = excluded.token_encrypted,
			token_fp = excluded.token_fp,
			expires_at = excluded.expires_at`,
		Args: []interface{}{t.Namespace, t.TopicID, t.Provider, encToken, tokenFP, expiresAt},
	}})
	if err != nil {
		return 0, fmt.Errorf("push topic: register: %w", err)
	}
	if !res.Committed {
		return 0, fmt.Errorf("push topic: register rolled back at op %d: %s", res.FailedIndex, batchFailure(res))
	}
	return expiresAt, nil
}

// batchFailure is the reason an uncommitted batch gives.
func batchFailure(res *rqlite.BatchResult) string {
	if res.Error != "" {
		return res.Error
	}
	if res.FailedIndex < len(res.Results) {
		return res.Results[res.FailedIndex].Error
	}
	return "no reason given"
}

// Unregister implements PushTopicStore.
func (s *RqliteTopicStore) Unregister(ctx context.Context, namespace, topicID string) error {
	if namespace == "" {
		return fmt.Errorf("push topic: namespace required")
	}
	if err := ValidateTopicID(topicID); err != nil {
		return err
	}
	res, err := s.db.Exec(ctx,
		`DELETE FROM push_topics WHERE namespace = ? AND topic_id = ?`, namespace, topicID)
	if err != nil {
		return fmt.Errorf("push topic: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("push topic: delete: rows affected: %w", err)
	}
	if n == 0 {
		return ErrTopicNotFound
	}
	return nil
}

// Get implements PushTopicStore. An expired row is treated as absent even
// before the next registration removes it.
func (s *RqliteTopicStore) Get(ctx context.Context, namespace, topicID string) (*PushTopic, error) {
	if namespace == "" {
		return nil, fmt.Errorf("push topic: namespace required")
	}
	if err := ValidateTopicID(topicID); err != nil {
		return nil, err
	}
	var rows []topicRow
	if err := s.db.Query(ctx, &rows,
		`SELECT provider, token_encrypted, expires_at
		   FROM push_topics
		  WHERE namespace = ? AND topic_id = ? AND expires_at > ?`,
		namespace, topicID, s.now().Unix()); err != nil {
		return nil, fmt.Errorf("push topic: query: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrTopicNotFound
	}
	token, err := secrets.Open(s.holder, TopicSecretsKeyPurpose, s.encKey, rows[0].TokenEncrypted)
	if err != nil {
		return nil, fmt.Errorf("push topic: decrypt token: %w", err)
	}
	return &PushTopic{
		Namespace: namespace,
		TopicID:   topicID,
		Provider:  rows[0].Provider,
		Token:     token,
		ExpiresAt: rows[0].ExpiresAt,
	}, nil
}
