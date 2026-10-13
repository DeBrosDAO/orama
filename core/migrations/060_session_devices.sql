-- Migration 060: device-bound sessions.
--
-- A session belonged to an account and nothing else. Every installation of an
-- application signed in as the same wallet held interchangeable sessions, so
-- losing one phone meant ending every session the account had, and nothing an
-- application ran could tell which installation was calling.
--
-- A device is now a key pair one installation holds — P-256 or Ed25519, where
-- the platforms keep a non-extractable key. Its id is the RFC 7638 thumbprint
-- of the public key, so it is computed, never asserted. A session may be bound
-- to one: it is issued only against a signature by the device's key, refreshed
-- only with a fresh proof from it, and ended — with every socket it holds open —
-- when the device is revoked, while the account's other devices stay signed in.
--
-- (059 is taken by push_topics on a parallel branch; this is the next number.)

-- The registry of devices. A row is never deleted: a revoked device keeps its
-- row as a tombstone, so its key can never be enrolled again — not by the
-- device, and not by anyone who copied its public key.
CREATE TABLE IF NOT EXISTS session_devices (
    -- The RFC 7638 thumbprint of the public key, base64url.
    id                 TEXT PRIMARY KEY,
    namespace_id       INTEGER NOT NULL,
    -- The account the device signs in as. One device, one account.
    subject            TEXT NOT NULL,
    -- The public JWK, exactly the members the thumbprint covers.
    public_key         TEXT NOT NULL,
    -- What the user called it, shown when they list their devices.
    label              TEXT NOT NULL DEFAULT '',
    -- pending: enrolled by a wallet signature in a namespace that requires an
    --          existing device's approval, and holding no session yet.
    -- active:  may hold sessions.
    -- revoked: holds nothing, and never will again.
    state              TEXT NOT NULL CHECK (state IN ('pending', 'active', 'revoked')),
    -- The device that approved this one, when one did.
    approved_by_device TEXT,
    created_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    activated_at       TIMESTAMP,
    revoked_at         TIMESTAMP,
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_session_devices_account ON session_devices(namespace_id, subject);

-- What a namespace requires of a session. No row is 'optional': a client may
-- bind a device and need not, which is how every namespace worked before.
CREATE TABLE IF NOT EXISTS namespace_session_policy (
    namespace_id  INTEGER PRIMARY KEY,
    -- optional: a device is bound when the client sends one.
    -- required: every end-user session is bound to a device.
    -- approval: as required, and a device beyond an account's first needs an
    --           existing device to approve it.
    device_policy TEXT NOT NULL CHECK (device_policy IN ('optional', 'required', 'approval')),
    updated_by    TEXT NOT NULL,
    updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);

-- The device a session is bound to, if any, and the session's own id, which
-- stays the same across refresh-token rotations so the session can be ended
-- as a whole — its access tokens and its sockets with it.
ALTER TABLE refresh_tokens ADD COLUMN device_id TEXT;
ALTER TABLE refresh_tokens ADD COLUMN session_id TEXT;
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_device ON refresh_tokens(device_id);

-- A pending login from a device with a key: the key the session will be bound
-- to, and the device that approved it. A keyed login is approved by one of the
-- account's active devices, never by a wallet signature alone.
ALTER TABLE device_authorizations ADD COLUMN device_key TEXT;
ALTER TABLE device_authorizations ADD COLUMN device_label TEXT;
ALTER TABLE device_authorizations ADD COLUMN approved_by_device TEXT;

-- The session device a push registration was made from, so revoking the
-- device and removing its push registrations are one fact.
ALTER TABLE push_devices ADD COLUMN session_device_id TEXT;
CREATE INDEX IF NOT EXISTS idx_push_devices_session_device ON push_devices(namespace, session_device_id);
