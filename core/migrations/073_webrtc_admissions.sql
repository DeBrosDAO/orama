-- =============================================================================
-- 073_webrtc_admissions.sql
--
-- Who a namespace lets into its WebRTC rooms (bugboard #726).
--
-- A namespace that sets webrtc_settings.require_admission has its SFUs admit a
-- peer to a room only on an admission its own functions issued for that room
-- and user (the webrtc_admit host function), that has not expired and has not
-- been revoked (webrtc_kick). Both tables live in the namespace's own database:
-- the policy and the grants are the tenant's, they are read on the join path of
-- the namespace's own gateways, and they survive an SFU restart because no SFU
-- holds them.
--
-- device_id '' admits the user from any device; a device id admits that device
-- only. A user's session bound to no device matches only the '' row.
-- muted persists a webrtc_mute across a rejoin; revoked_at is a kick.
-- Times are unix seconds.
-- =============================================================================

CREATE TABLE IF NOT EXISTS webrtc_settings (
    namespace         TEXT    NOT NULL PRIMARY KEY,
    require_admission INTEGER NOT NULL DEFAULT 0,
    updated_at        INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS webrtc_admissions (
    namespace  TEXT    NOT NULL,
    room       TEXT    NOT NULL,
    user_id    TEXT    NOT NULL,
    device_id  TEXT    NOT NULL DEFAULT '',
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER,
    muted      INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (namespace, room, user_id, device_id)
);

CREATE INDEX IF NOT EXISTS idx_webrtc_admissions_expires_at
    ON webrtc_admissions(namespace, expires_at);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (73);
