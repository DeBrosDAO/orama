-- 036_device_bindings.sql
--
-- Device attribution (bugboard feat-384).
--
-- A serverless function could previously learn only WHICH ACCOUNT was calling:
-- every device of an account produced an identical JWT subject. That is enough
-- for delivery (the app controls fan-out) but not for retrieval, which is
-- authenticated per account — anyone holding the account seed could call the
-- history endpoints as the account without being a current device.
--
-- The gateway now binds a device public key to a login when the client presents
-- a device assertion, and stamps an unforgeable device claim into the JWT.
--
-- first_seen_at is the load-bearing column. The product rule it supports is "a
-- device added to an account syncs forward and never receives the archive",
-- which is meant to bound the damage of a stolen recovery phrase. An app's own
-- device roster CANNOT bound that case when the roster is signed by a key
-- derived from the seed: an attacker holding the seed signs a roster backdating
-- their device to genesis and pulls everything. A gateway-observed first-seen is
-- the one timestamp such an attacker cannot move, because it records when THIS
-- server first saw the key — not what a signed document claims.
--
-- Named with the orama_ prefix deliberately. These migrations also run inside
-- each tenant's namespace RQLite, and "device_bindings" is exactly the table a
-- device-roster app would create for itself; an unprefixed name would let a
-- pre-existing tenant table win CREATE TABLE IF NOT EXISTS and silently break
-- every device login. Follows the orama_schema_migrations precedent.
--
-- The gateway deliberately stores no notion of a roster or of device
-- authorization. It asserts possession ("this key signed this login"); the
-- namespace's own function decides whether that device is currently allowed.
CREATE TABLE IF NOT EXISTS orama_device_bindings (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace_id  INTEGER NOT NULL,
    -- subject is the account identity the device authenticated as: the same
    -- value that lands in the JWT `sub`.
    subject       TEXT NOT NULL,
    -- device_fp is the fingerprint the JWT carries and functions compare
    -- against their roster. Derived from public_key, never client-supplied.
    device_fp     TEXT NOT NULL,
    -- public_key is the raw device key (base64), kept so a binding can be
    -- re-verified and so an operator can audit what was bound.
    public_key    TEXT NOT NULL,
    -- first_seen_at is set once, on the first successful assertion, and is
    -- never updated afterwards. See the note above: its value is that nothing
    -- the client controls can move it backwards.
    first_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- revoked_at is set by the namespace when it removes the device. A revoked
    -- binding stops minting device claims AND its refresh tokens are revoked,
    -- so the device is out within one access-token TTL rather than riding the
    -- 30-day refresh chain.
    revoked_at    TIMESTAMP,
    UNIQUE(namespace_id, subject, device_fp),
    FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_orama_device_bindings_subject
    ON orama_device_bindings(namespace_id, subject);

-- Binds a refresh token to the device that obtained it.
--
-- Without this the device claim would ride the refresh chain for the refresh
-- token's full 30-day life: revoking a device would stop new logins but the
-- already-issued chain would keep minting fresh, validly-signed, device-stamped
-- access tokens. Revocation targets these rows, which is what makes "a revoked
-- device stops being served" true within one 15-minute access token rather than
-- in 30 days. NULL = a login that presented no device assertion.
ALTER TABLE refresh_tokens ADD COLUMN device_fp TEXT;
