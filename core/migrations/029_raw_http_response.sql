-- =============================================================================
-- 029_raw_http_response.sql
--
-- Raw-HTTP-response serverless function mode — bugboard #835.
--
-- When raw_http_response is true, the function may call the set_http_response
-- host function to emit a verbatim HTTP response (status + headers + body)
-- instead of the JSON/Ack-wrapped output. This lets a namespace app proxy an
-- upstream RPC (Helius / Alchemy) transparently. See pkg/serverless/raw_http.go.
--
-- Default false → backward compatible: existing functions keep returning the
-- JSON/Ack-wrapped output unchanged.
-- =============================================================================

ALTER TABLE functions ADD COLUMN raw_http_response BOOLEAN DEFAULT FALSE;
