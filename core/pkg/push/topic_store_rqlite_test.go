package push

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

const (
	topicTestNS          = "anchat"
	topicTestToken       = "apns-device-token-0001"
	topicTestFingerprint = "topic-test-cluster-secret-for-fingerprints"
)

// topicMigration is the real migration file, so the tests exercise the schema
// that ships rather than a copy of it.
func topicMigration(t *testing.T) string {
	t.Helper()
	ddl, err := migrations.FS.ReadFile("059_push_topics.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return string(ddl)
}

// newTopicTestStore builds an RqliteTopicStore over in-memory SQLite with a
// transactional Batch, and a clock the test moves.
func newTopicTestStore(t *testing.T, extraDDL ...string) (*RqliteTopicStore, *sql.DB, *time.Time) {
	t.Helper()
	client, db := rqlitetest.SQLite(t, append([]string{topicMigration(t)}, extraDDL...)...)
	store, err := NewRqliteTopicStore(client, testClusterSecret, topicTestFingerprint)
	if err != nil {
		t.Fatalf("new topic store: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	store.now = func() time.Time { return now }
	return store, db, &now
}

func topicIDFor(t *testing.T, seed byte) string {
	t.Helper()
	id, err := TopicIDFromSecret(strings.Repeat(fmt.Sprintf("%02x", seed), 32))
	if err != nil {
		t.Fatalf("TopicIDFromSecret: %v", err)
	}
	return id
}

func mustRegisterTopic(t *testing.T, s *RqliteTopicStore, ns, topicID, token string) int64 {
	t.Helper()
	exp, err := s.Register(context.Background(), PushTopic{Namespace: ns, TopicID: topicID, Provider: "apns", Token: token})
	if err != nil {
		t.Fatalf("register %s: %v", topicID, err)
	}
	return exp
}

func topicCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM push_topics`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestRqliteTopicStore_Register_happyPath(t *testing.T) {
	s, db, now := newTopicTestStore(t)
	id := topicIDFor(t, 1)

	exp := mustRegisterTopic(t, s, topicTestNS, id, topicTestToken)
	if min := now.Add(TopicTTL).Unix(); exp < min {
		t.Errorf("expires_at = %d, earlier than now+TopicTTL = %d", exp, min)
	}

	got, err := s.Get(context.Background(), topicTestNS, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Token != topicTestToken || got.Provider != "apns" || got.ExpiresAt != exp {
		t.Errorf("Get = %+v", got)
	}

	var enc string
	if err := db.QueryRow(`SELECT token_encrypted FROM push_topics WHERE topic_id = ?`, id).Scan(&enc); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if strings.Contains(enc, topicTestToken) {
		t.Error("the provider token is stored in plaintext")
	}
}

// The stored expiry is whole days: it must not record when the device
// registered, only a day it expires on, and never less than TopicTTL away.
func TestExpiryFrom_roundsUpToGranularity(t *testing.T) {
	granule := int64(TopicExpiryGranularity / time.Second)
	for _, now := range []time.Time{
		time.Unix(1_800_000_000, 0),
		time.Unix(1_800_000_001, 0),
		time.Unix(1_800_000_000, 0).Truncate(TopicExpiryGranularity),
	} {
		exp := expiryFrom(now)
		if exp%granule != 0 {
			t.Errorf("expiry %d for %v is not a whole %v", exp, now, TopicExpiryGranularity)
		}
		lo, hi := now.Add(TopicTTL).Unix(), now.Add(TopicTTL+TopicExpiryGranularity).Unix()
		if exp < lo || exp >= hi {
			t.Errorf("expiry %d for %v outside [%d, %d)", exp, now, lo, hi)
		}
	}
	// Two registrations in the same day store the same expiry.
	a := time.Unix(1_800_000_000, 0).Truncate(TopicExpiryGranularity).Add(time.Hour)
	if expiryFrom(a) != expiryFrom(a.Add(5*time.Hour)) {
		t.Error("registrations hours apart in one day are distinguishable by expiry")
	}
}

// The table must not be able to hold who a topic belongs to, or when exactly
// it was registered.
func TestRqliteTopicStore_tableHasNoIdentityOrTimeColumn(t *testing.T) {
	_, db, _ := newTopicTestStore(t)
	rows, err := db.Query(`SELECT name FROM pragma_table_info('push_topics')`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, banned := range []string{"user", "subject", "sub", "wallet", "account", "secret", "created", "updated"} {
			if strings.Contains(col, banned) {
				t.Errorf("push_topics has column %q, which could tie a topic to an account or a moment", col)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	// Nor an insertion order: WITHOUT ROWID has no rowid to read.
	if _, err := db.Exec(`SELECT rowid FROM push_topics`); err == nil {
		t.Error("push_topics has a rowid, which records the order topics were registered in")
	}
}

func TestRqliteTopicStore_Register_refreshMovesExpiryAndToken(t *testing.T) {
	s, _, now := newTopicTestStore(t)
	id := topicIDFor(t, 1)
	first := mustRegisterTopic(t, s, topicTestNS, id, topicTestToken)

	*now = now.Add(3 * 24 * time.Hour)
	exp := mustRegisterTopic(t, s, topicTestNS, id, "apns-device-token-rotated")

	got, err := s.Get(context.Background(), topicTestNS, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if exp <= first || got.ExpiresAt != exp {
		t.Errorf("refresh did not move the expiry: first %d, refreshed %d, stored %d", first, exp, got.ExpiresAt)
	}
	if got.Token != "apns-device-token-rotated" {
		t.Errorf("refresh did not re-point the token: %q", got.Token)
	}
}

// One provider token, one topic row: registering the device's token under a
// rotated topic removes the previous topic.
func TestRqliteTopicStore_Register_rotationReplacesPreviousTopic(t *testing.T) {
	s, db, _ := newTopicTestStore(t)
	oldID, newID := topicIDFor(t, 1), topicIDFor(t, 2)
	mustRegisterTopic(t, s, topicTestNS, oldID, topicTestToken)
	mustRegisterTopic(t, s, topicTestNS, newID, topicTestToken)

	if _, err := s.Get(context.Background(), topicTestNS, oldID); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("old topic still live after rotation: err=%v", err)
	}
	if _, err := s.Get(context.Background(), topicTestNS, newID); err != nil {
		t.Errorf("new topic: %v", err)
	}
	if n := topicCount(t, db); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

// The eviction and the upsert are one write: if the upsert fails, the device's
// previous topic is still there.
func TestRqliteTopicStore_Register_failedUpsertKeepsPreviousTopic(t *testing.T) {
	failID := topicIDFor(t, 2)
	trigger := fmt.Sprintf(`CREATE TRIGGER fail_insert BEFORE INSERT ON push_topics
		WHEN NEW.topic_id = '%s' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`, failID)
	s, db, _ := newTopicTestStore(t, trigger)
	oldID := topicIDFor(t, 1)
	mustRegisterTopic(t, s, topicTestNS, oldID, topicTestToken)

	_, err := s.Register(context.Background(), PushTopic{Namespace: topicTestNS, TopicID: failID, Provider: "apns", Token: topicTestToken})
	if err == nil {
		t.Fatal("register succeeded despite the failing insert")
	}
	if _, err := s.Get(context.Background(), topicTestNS, oldID); err != nil {
		t.Errorf("the previous topic was evicted by a registration that failed: %v", err)
	}
	if n := topicCount(t, db); n != 1 {
		t.Errorf("rows = %d, want the previous topic only", n)
	}
}

func TestRqliteTopicStore_Register_leavesOtherNamespacesAlone(t *testing.T) {
	s, db, _ := newTopicTestStore(t)
	mustRegisterTopic(t, s, "tenant-a", topicIDFor(t, 1), topicTestToken)
	mustRegisterTopic(t, s, "tenant-b", topicIDFor(t, 2), topicTestToken)

	if n := topicCount(t, db); n != 2 {
		t.Errorf("rows = %d, want 2: eviction crossed a namespace", n)
	}
}

func TestRqliteTopicStore_Get_expiredIsNotFound(t *testing.T) {
	s, _, now := newTopicTestStore(t)
	id := topicIDFor(t, 1)
	exp := mustRegisterTopic(t, s, topicTestNS, id, topicTestToken)

	*now = time.Unix(exp, 0)
	if _, err := s.Get(context.Background(), topicTestNS, id); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("Get at expiry: err=%v, want ErrTopicNotFound", err)
	}
}

// Registration is the sweep: expired rows go the next time anyone registers.
func TestRqliteTopicStore_Register_prunesExpiredTopics(t *testing.T) {
	s, db, now := newTopicTestStore(t)
	mustRegisterTopic(t, s, topicTestNS, topicIDFor(t, 1), "token-one")
	exp := mustRegisterTopic(t, s, topicTestNS, topicIDFor(t, 2), "token-two")

	*now = time.Unix(exp, 0)
	mustRegisterTopic(t, s, topicTestNS, topicIDFor(t, 3), "token-three")

	if n := topicCount(t, db); n != 1 {
		t.Errorf("rows = %d after registering past expiry, want 1 (the expired two pruned)", n)
	}
}

func TestRqliteTopicStore_Get_unknownIsNotFound(t *testing.T) {
	s, _, _ := newTopicTestStore(t)
	if _, err := s.Get(context.Background(), topicTestNS, topicIDFor(t, 9)); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("err = %v, want ErrTopicNotFound", err)
	}
}

func TestRqliteTopicStore_Unregister_removesOnlyItsOwnNamespace(t *testing.T) {
	s, db, _ := newTopicTestStore(t)
	id := topicIDFor(t, 1)
	mustRegisterTopic(t, s, topicTestNS, id, topicTestToken)

	if err := s.Unregister(context.Background(), "another-tenant", id); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("unregister from another namespace: err=%v, want ErrTopicNotFound", err)
	}
	if err := s.Unregister(context.Background(), topicTestNS, id); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if n := topicCount(t, db); n != 0 {
		t.Errorf("rows = %d after unregister", n)
	}
	if err := s.Unregister(context.Background(), topicTestNS, id); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("second unregister: err=%v, want ErrTopicNotFound", err)
	}
}

func TestRqliteTopicStore_rejectsInvalidInput(t *testing.T) {
	s, db, _ := newTopicTestStore(t)
	ctx := context.Background()
	id := topicIDFor(t, 1)
	cases := map[string]PushTopic{
		"no namespace": {TopicID: id, Provider: "apns", Token: "t"},
		"no provider":  {Namespace: topicTestNS, TopicID: id, Token: "t"},
		"bad topic id": {Namespace: topicTestNS, TopicID: "not-a-digest", Provider: "apns", Token: "t"},
		"empty token":  {Namespace: topicTestNS, TopicID: id, Provider: "apns"},
	}
	for name, topic := range cases {
		if _, err := s.Register(ctx, topic); err == nil {
			t.Errorf("%s: registered", name)
		}
	}
	if _, err := s.Get(ctx, topicTestNS, "UPPER"); !errors.Is(err, ErrInvalidTopicID) {
		t.Errorf("Get with a bad id: %v", err)
	}
	if err := s.Unregister(ctx, "", id); err == nil {
		t.Error("Unregister without a namespace succeeded")
	}
	if n := topicCount(t, db); n != 0 {
		t.Errorf("rows = %d after only invalid input", n)
	}
}

func TestNewRqliteTopicStore_requiresFingerprintSecret(t *testing.T) {
	client, _ := rqlitetest.SQLite(t)
	if _, err := NewRqliteTopicStore(client, testClusterSecret, ""); err == nil {
		t.Error("built a store with no fingerprint secret")
	}
}

// A secrets rotate re-encrypts push_topics through the column listed in
// secrets.NamespaceColumns. After it, a gateway started on the new root must
// still read the token AND still recognise it: rotating the device's topic
// has to evict the topic registered before the rotate.
func TestRqliteTopicStore_secretsRotateKeepsReadAndEviction(t *testing.T) {
	client, db := rqlitetest.SQLite(t, topicMigration(t))
	before, err := NewRqliteTopicStore(client, testClusterSecret, topicTestFingerprint)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	before.SetHolder(secrets.NewHolder(secrets.Root{CurrentID: "1", CurrentIKM: testClusterSecret}))
	oldID := topicIDFor(t, 1)
	mustRegisterTopic(t, before, topicTestNS, oldID, topicTestToken)

	walkPushTopics(t, db, secrets.Root{CurrentID: "2", CurrentIKM: "rotated-ikm", PreviousID: "1",
		PreviousIKM: testClusterSecret, WriteVersioned: true})

	// A gateway restarted after the rotate: new IKM, same cluster secret.
	after, err := NewRqliteTopicStore(client, "rotated-ikm", topicTestFingerprint)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	after.SetHolder(secrets.NewHolder(secrets.Root{CurrentID: "2", CurrentIKM: "rotated-ikm", WriteVersioned: true}))
	if got, err := after.Get(context.Background(), topicTestNS, oldID); err != nil || got.Token != topicTestToken {
		t.Fatalf("after rotate: %+v, %v", got, err)
	}
	mustRegisterTopic(t, after, topicTestNS, topicIDFor(t, 2), topicTestToken)
	if _, err := after.Get(context.Background(), topicTestNS, oldID); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("topic registered before the rotate survived the device's rotation: %v", err)
	}
}

func walkPushTopics(t *testing.T, db *sql.DB, root secrets.Root) {
	t.Helper()
	var cols []secrets.Column
	for _, c := range secrets.NamespaceColumns() {
		if c.Table == "push_topics" {
			cols = append(cols, c)
		}
	}
	if len(cols) != 1 {
		t.Fatalf("secrets.NamespaceColumns lists push_topics %d times, want once", len(cols))
	}
	res, err := secrets.Walk(context.Background(), rqlite.NewClient(db), root, cols)
	if err != nil || res.Rewrote != 1 || len(res.Failures) != 0 {
		t.Fatalf("walk = %+v, %v; want one row rewritten", res, err)
	}
}

// A device token's fingerprint in push_topics must not match its fingerprint
// in push_devices, or the two tables could be joined to link a topic to an
// account.
func TestRqliteTopicStore_fingerprintDiffersFromDeviceStore(t *testing.T) {
	topics, _, _ := newTopicTestStore(t)
	devices, _ := newDeviceTestStore(t)
	if topics.tokenFingerprint(topicTestNS, topicTestToken) == devices.tokenFingerprint(topicTestToken) {
		t.Error("push_topics and push_devices fingerprint a token identically")
	}
	if topics.tokenFingerprint("tenant-a", topicTestToken) == topics.tokenFingerprint("tenant-b", topicTestToken) {
		t.Error("one token fingerprints the same in two namespaces")
	}
}

// Against a real rqlited: Batch goes through rqlite's transactional endpoint
// and the types come back through gorqlite. Skipped when rqlited is absent.
func TestRqliteTopicStore_live_registerRotateUnregister(t *testing.T) {
	client := rqlitetest.Start(t)
	ctx := context.Background()
	for _, stmt := range strings.Split(topicMigration(t), ";") {
		if strings.Contains(stmt, "CREATE") {
			if _, err := client.Exec(ctx, stmt); err != nil {
				t.Fatalf("apply migration: %v", err)
			}
		}
	}
	s, err := NewRqliteTopicStore(client, testClusterSecret, topicTestFingerprint)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	oldID, newID := topicIDFor(t, 1), topicIDFor(t, 2)
	exp := mustRegisterTopic(t, s, topicTestNS, oldID, topicTestToken)
	got, err := s.Get(ctx, topicTestNS, oldID)
	if err != nil || got.Token != topicTestToken || got.ExpiresAt != exp {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	mustRegisterTopic(t, s, topicTestNS, newID, topicTestToken)
	if _, err := s.Get(ctx, topicTestNS, oldID); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("old topic after rotation: %v", err)
	}
	if err := s.Unregister(ctx, topicTestNS, newID); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if err := s.Unregister(ctx, topicTestNS, newID); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("second unregister: %v", err)
	}
}
