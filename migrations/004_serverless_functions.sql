-- Orama Network - Serverless Functions Engine (Phase 4)
-- WASM-based serverless function execution with triggers, jobs, and secrets

BEGIN;

-- =============================================================================
-- FUNCTIONS TABLE
-- Core function registry with versioning support
-- =============================================================================
CREATE TABLE IF NOT EXISTS functions (
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
    UNIQUE(namespace, name)
);

CREATE INDEX IF NOT EXISTS idx_functions_namespace ON functions(namespace);
CREATE INDEX IF NOT EXISTS idx_functions_name ON functions(namespace, name);
CREATE INDEX IF NOT EXISTS idx_functions_status ON functions(status);

-- =============================================================================
-- FUNCTION ENVIRONMENT VARIABLES
-- Non-sensitive configuration per function
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_env_vars (
    id          TEXT PRIMARY KEY,
    function_id TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(function_id, key),
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_env_vars_function ON function_env_vars(function_id);

-- =============================================================================
-- FUNCTION SECRETS
-- Encrypted secrets per namespace (shared across functions in namespace)
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_secrets (
    id              TEXT PRIMARY KEY,
    namespace       TEXT NOT NULL,
    name            TEXT NOT NULL,
    encrypted_value BLOB NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(namespace, name)
);

CREATE INDEX IF NOT EXISTS idx_function_secrets_namespace ON function_secrets(namespace);

-- =============================================================================
-- CRON TRIGGERS
-- Scheduled function execution using cron expressions
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_cron_triggers (
    id              TEXT PRIMARY KEY,
    function_id     TEXT NOT NULL,
    cron_expression TEXT NOT NULL,
    next_run_at     TIMESTAMP,
    last_run_at     TIMESTAMP,
    last_status     TEXT,
    last_error      TEXT,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_cron_triggers_function ON function_cron_triggers(function_id);
CREATE INDEX IF NOT EXISTS idx_function_cron_triggers_next_run ON function_cron_triggers(next_run_at) 
    WHERE enabled = TRUE;

-- =============================================================================
-- DATABASE TRIGGERS
-- Trigger functions on database changes (INSERT/UPDATE/DELETE)
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_db_triggers (
    id          TEXT PRIMARY KEY,
    function_id TEXT NOT NULL,
    table_name  TEXT NOT NULL,
    operation   TEXT NOT NULL CHECK(operation IN ('INSERT', 'UPDATE', 'DELETE')),
    condition   TEXT,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_db_triggers_function ON function_db_triggers(function_id);
CREATE INDEX IF NOT EXISTS idx_function_db_triggers_table ON function_db_triggers(table_name, operation) 
    WHERE enabled = TRUE;

-- =============================================================================
-- PUBSUB TRIGGERS
-- Trigger functions on pubsub messages
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_pubsub_triggers (
    id          TEXT PRIMARY KEY,
    function_id TEXT NOT NULL,
    topic       TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_pubsub_triggers_function ON function_pubsub_triggers(function_id);
CREATE INDEX IF NOT EXISTS idx_function_pubsub_triggers_topic ON function_pubsub_triggers(topic) 
    WHERE enabled = TRUE;

-- =============================================================================
-- ONE-TIME TIMERS
-- Schedule functions to run once at a specific time
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_timers (
    id          TEXT PRIMARY KEY,
    function_id TEXT NOT NULL,
    run_at      TIMESTAMP NOT NULL,
    payload     TEXT,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'running', 'completed', 'failed')),
    error       TEXT,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_timers_function ON function_timers(function_id);
CREATE INDEX IF NOT EXISTS idx_function_timers_pending ON function_timers(run_at) 
    WHERE status = 'pending';

-- =============================================================================
-- BACKGROUND JOBS
-- Long-running async function execution
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_jobs (
    id           TEXT PRIMARY KEY,
    function_id  TEXT NOT NULL,
    payload      TEXT,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    progress     INTEGER NOT NULL DEFAULT 0 CHECK(progress >= 0 AND progress <= 100),
    result       TEXT,
    error        TEXT,
    started_at   TIMESTAMP,
    completed_at TIMESTAMP,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_jobs_function ON function_jobs(function_id);
CREATE INDEX IF NOT EXISTS idx_function_jobs_status ON function_jobs(status);
CREATE INDEX IF NOT EXISTS idx_function_jobs_pending ON function_jobs(created_at) 
    WHERE status = 'pending';

-- =============================================================================
-- INVOCATION LOGS
-- Record of all function invocations for debugging and metrics
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_invocations (
    id              TEXT PRIMARY KEY,
    function_id     TEXT NOT NULL,
    request_id      TEXT NOT NULL,
    trigger_type    TEXT NOT NULL,
    caller_wallet   TEXT,
    input_size      INTEGER,
    output_size     INTEGER,
    started_at      TIMESTAMP NOT NULL,
    completed_at    TIMESTAMP,
    duration_ms     INTEGER,
    status          TEXT CHECK(status IN ('success', 'error', 'timeout')),
    error_message   TEXT,
    memory_used_mb  REAL,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_invocations_function ON function_invocations(function_id);
CREATE INDEX IF NOT EXISTS idx_function_invocations_request ON function_invocations(request_id);
CREATE INDEX IF NOT EXISTS idx_function_invocations_time ON function_invocations(started_at);
CREATE INDEX IF NOT EXISTS idx_function_invocations_status ON function_invocations(function_id, status);

-- =============================================================================
-- FUNCTION LOGS
-- Captured log output from function execution
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_logs (
    id            TEXT PRIMARY KEY,
    function_id   TEXT NOT NULL,
    invocation_id TEXT NOT NULL,
    level         TEXT NOT NULL CHECK(level IN ('info', 'warn', 'error', 'debug')),
    message       TEXT NOT NULL,
    timestamp     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (function_id) REFERENCES functions(id) ON DELETE CASCADE,
    FOREIGN KEY (invocation_id) REFERENCES function_invocations(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_function_logs_invocation ON function_logs(invocation_id);
CREATE INDEX IF NOT EXISTS idx_function_logs_function ON function_logs(function_id, timestamp);

-- =============================================================================
-- DB CHANGE TRACKING
-- Track last processed row for database triggers (CDC-like)
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_db_change_tracking (
    id              TEXT PRIMARY KEY,
    trigger_id      TEXT NOT NULL UNIQUE,
    last_row_id     INTEGER,
    last_updated_at TIMESTAMP,
    last_check_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (trigger_id) REFERENCES function_db_triggers(id) ON DELETE CASCADE
);

-- =============================================================================
-- RATE LIMITING
-- Track request counts for rate limiting
-- =============================================================================
CREATE TABLE IF NOT EXISTS function_rate_limits (
    id          TEXT PRIMARY KEY,
    window_key  TEXT NOT NULL,
    count       INTEGER NOT NULL DEFAULT 0,
    window_start TIMESTAMP NOT NULL,
    UNIQUE(window_key, window_start)
);

CREATE INDEX IF NOT EXISTS idx_function_rate_limits_window ON function_rate_limits(window_key, window_start);

-- =============================================================================
-- MIGRATION VERSION TRACKING
-- =============================================================================
INSERT OR IGNORE INTO schema_migrations(version) VALUES (4);

COMMIT;

