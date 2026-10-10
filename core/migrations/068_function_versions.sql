-- Migration 068: a function keeps every version it was deployed as.
--
-- `functions` was declared UNIQUE(namespace, name) and the registry wrote with
-- INSERT OR REPLACE, so a deploy overwrote the row of the version before it:
-- `name@1` stopped existing the moment `name@2` was deployed, and `orama
-- function versions` could only ever list one. The key is now
-- (namespace, name, version); the registry inserts a new row per deploy.
--
-- SQLite cannot change a UNIQUE constraint in place, so the table is rebuilt:
-- every column, row and index is carried across, and the ids are unchanged, so
-- the child tables that reference functions(id) keep pointing at the same rows.
-- The copy is INSERT OR IGNORE into a table with the same columns, so a re-run
-- of this file on a table already rebuilt copies it onto itself and changes
-- nothing.

CREATE TABLE IF NOT EXISTS functions_new (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    namespace       TEXT NOT NULL,
    version         INTEGER NOT NULL DEFAULT 1,
    wasm_cid        TEXT NOT NULL,
    source_cid      TEXT,
    memory_limit_mb INTEGER NOT NULL DEFAULT 64,
    timeout_seconds INTEGER NOT NULL DEFAULT 30,
    is_public       BOOLEAN NOT NULL DEFAULT FALSE,
    retry_count     INTEGER NOT NULL DEFAULT 0,
    retry_delay_seconds INTEGER NOT NULL DEFAULT 5,
    dlq_topic       TEXT,
    status          TEXT NOT NULL DEFAULT 'active',
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by      TEXT NOT NULL,
    ws_persistent   BOOLEAN DEFAULT FALSE,
    ws_idle_timeout_sec INTEGER DEFAULT 0,
    ws_max_frame_bytes  INTEGER DEFAULT 0,
    ws_max_inflight_per_conn INTEGER DEFAULT 0,
    raw_http_response BOOLEAN DEFAULT FALSE,
    is_internal     BOOLEAN NOT NULL DEFAULT FALSE,
    ws_auth         TEXT NOT NULL DEFAULT '',
    UNIQUE(namespace, name, version)
);

INSERT OR IGNORE INTO functions_new (
    id, name, namespace, version, wasm_cid, source_cid,
    memory_limit_mb, timeout_seconds, is_public,
    retry_count, retry_delay_seconds, dlq_topic,
    status, created_at, updated_at, created_by,
    ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn,
    raw_http_response, is_internal, ws_auth
)
SELECT
    id, name, namespace, version, wasm_cid, source_cid,
    memory_limit_mb, timeout_seconds, is_public,
    retry_count, retry_delay_seconds, dlq_topic,
    status, created_at, updated_at, created_by,
    ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn,
    raw_http_response, is_internal, ws_auth
FROM functions;

DROP TABLE IF EXISTS functions;
ALTER TABLE functions_new RENAME TO functions;

CREATE INDEX IF NOT EXISTS idx_functions_namespace ON functions(namespace);
CREATE INDEX IF NOT EXISTS idx_functions_name ON functions(namespace, name);
CREATE INDEX IF NOT EXISTS idx_functions_status ON functions(status);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (68);
