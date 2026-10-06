-- Migration 048: make audit_events usable.
--
-- The table has existed since 002_core.sql and has never been written to. The
-- only reference to it anywhere is the namespace delete handler's list of
-- tables to clean up. So there is no record of who minted a key, who was given
-- a grant, who revoked what, or who signed in — nothing to answer "when did
-- this credential appear" with, which is the first question anyone asks.
--
-- Its shape did not fit the events worth recording:
--
--   * namespace_id is NOT NULL with a foreign key, so an event that has no
--     namespace could not be written at all — and a login attempt naming a
--     namespace that does not exist is exactly an event worth having.
--   * there was nowhere to say whether the thing succeeded, so "show me the
--     failed logins" meant parsing a JSON blob.
--   * there was nowhere for the user agent.
--
-- The table is empty on every cluster, so it is replaced rather than migrated.
BEGIN;

DROP TABLE IF EXISTS audit_events;

CREATE TABLE IF NOT EXISTS audit_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,

    -- The namespace the event belongs to, by name. NULL for a cluster-level
    -- event, and a name rather than an id so an attempt against a namespace
    -- that does not exist is still recorded.
    namespace    TEXT,

    -- Who did it: a wallet, an API key's id, or "system" when the gateway
    -- acted on its own behalf.
    actor        TEXT,

    -- What they did. One of a fixed set; see pkg/gateway/auth/audit.go.
    action       TEXT NOT NULL,

    -- What it was done to, when that is not the actor: a key id, a namespace.
    resource     TEXT,

    -- "success" or "failure". A column rather than a field in the blob,
    -- because "show me the failures" is the query this table exists for.
    result       TEXT NOT NULL DEFAULT 'success',

    ip           TEXT,
    user_agent   TEXT,

    -- Anything else worth keeping, as JSON. Never a credential.
    metadata     TEXT,

    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_ns_time ON audit_events(namespace, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_events(action);
CREATE INDEX IF NOT EXISTS idx_audit_result ON audit_events(result, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_events(actor);

COMMIT;
