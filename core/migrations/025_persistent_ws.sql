-- =============================================================================
-- 025_persistent_ws.sql
--
-- Persistent WebSocket function settings — see plan
--   core/plans/platform/06_PERSISTENT_WS_FUNCTIONS.md
--
-- When ws_persistent is true, the function is bound to a single WebSocket
-- connection for its lifetime; exports ws_open / ws_frame / ws_close instead
-- of the default _start. See pkg/serverless/persistent for runtime details.
--
-- All defaults are zero / false → backward compatible: existing functions
-- continue to use the per-frame stateless WS model.
-- =============================================================================

ALTER TABLE functions ADD COLUMN ws_persistent BOOLEAN DEFAULT FALSE;
ALTER TABLE functions ADD COLUMN ws_idle_timeout_sec INTEGER DEFAULT 0;
ALTER TABLE functions ADD COLUMN ws_max_frame_bytes INTEGER DEFAULT 0;
ALTER TABLE functions ADD COLUMN ws_max_inflight_per_conn INTEGER DEFAULT 0;
