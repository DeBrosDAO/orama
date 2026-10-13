-- Migration 061: how a function's WebSocket may be opened (feat-264).
--
-- Every function's WebSocket was opened on the caller's credential, so the
-- node that terminated it learned which account was connecting. A function
-- may now declare `ws_auth: capability`: its socket may then also be opened
-- with a capability — a token one of the namespace's devices minted through
-- the function itself — which names no account.
--
-- '' is a credential, as before, so every existing function keeps its
-- behaviour. The column is nullable-free with a default, and an older gateway
-- that does not read it serves every function the old way.

ALTER TABLE functions ADD COLUMN ws_auth TEXT NOT NULL DEFAULT '';
