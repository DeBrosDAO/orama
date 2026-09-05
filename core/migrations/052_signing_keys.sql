-- Migration 052: the public half of every JWT signing key in the cluster.
--
-- The Ed25519 signing key was HKDF-derived from the cluster secret with a fixed
-- label, so every node and every namespace gateway held the private key that
-- signs tokens for every namespace. A compromised namespace gateway could mint
-- a token for any tenant, and there was nothing to rotate to: one derivation,
-- one key, for ever.
--
-- Each gateway generates its own key now and publishes the public half here.
-- The namespace column is what binds a key to a tenant: a token whose `kid`
-- names a namespace-bound key is refused unless its `namespace` claim matches.
-- NULL is the index gateway's key, which is the control plane and is bound to
-- nothing.

CREATE TABLE IF NOT EXISTS signing_keys (
    -- The `kid` in a token header. Derived from the public key, so two
    -- gateways cannot collide and a key's id cannot be chosen.
    kid          TEXT PRIMARY KEY,

    -- The namespace this key may sign for, or NULL for the index gateway.
    namespace    TEXT,

    -- "EdDSA". Recorded rather than assumed, so a future algorithm does not
    -- have to be told apart by the length of the key.
    algorithm    TEXT NOT NULL DEFAULT 'EdDSA',

    -- The public key, base64url, exactly as it appears in the JWKS.
    public_key   TEXT NOT NULL,

    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- When this key stops being accepted. Set when a successor is published:
    -- a rotation publishes the new key, signs with it, and leaves the old one
    -- verifiable for one access-token lifetime so nothing already issued
    -- breaks. NULL means live.
    retired_at   TIMESTAMP
);

-- The lookup every verification does: kid to key.
-- (The primary key already serves it; this is the one for "which keys does
-- this namespace have", which rotation and the JWKS need.)
CREATE INDEX IF NOT EXISTS idx_signing_keys_namespace ON signing_keys(namespace, retired_at);
