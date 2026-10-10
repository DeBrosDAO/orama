-- =============================================================================
-- 059_push_topics.sql
--
-- Push registrations addressed by a rotating topic instead of an account
-- (FEAT-265).
--
-- A device picks a random secret, and topic_id is the lowercase hex SHA-256 of
-- it. The device hands topic_id to its contacts and keeps the secret: senders
-- address the topic, and only the holder of the secret can register, refresh or
-- remove it. The secret itself is never stored.
--
-- There is deliberately no user_id, subject or wallet column. Nothing in this
-- table says which account a topic belongs to. There is no created_at either,
-- expires_at is rounded up to a whole day, and the table is WITHOUT ROWID so
-- rows are kept in topic_id order rather than insertion order: an exact
-- registration time or order could be joined against request logs to find who
-- registered the topic.
--
-- token_encrypted is AES-256-GCM ciphertext, sealed exactly as push_devices
-- seals its tokens but under its own key purpose. token_fp is a keyed HMAC of
-- the plaintext token, under a key distinct from push_devices.token_fp, so a
-- provider token maps to one topic row here and the two tables' fingerprints
-- cannot be joined. Rows past expires_at are never delivered to and are removed
-- by the next registration in the namespace.
-- =============================================================================

CREATE TABLE IF NOT EXISTS push_topics (
    namespace       TEXT    NOT NULL,
    topic_id        TEXT    NOT NULL,
    provider        TEXT    NOT NULL,
    token_encrypted TEXT    NOT NULL,
    token_fp        TEXT    NOT NULL,
    expires_at      INTEGER NOT NULL,
    PRIMARY KEY (namespace, topic_id)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_push_topics_token_fp
    ON push_topics(namespace, token_fp);

CREATE INDEX IF NOT EXISTS idx_push_topics_expires_at
    ON push_topics(namespace, expires_at);
