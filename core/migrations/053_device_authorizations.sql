-- Migration 053: pending device logins.
--
-- `orama auth login` needed the RootWallet desktop app on the same machine: it
-- shelled out to `rw` to sign the gateway's challenge. On a server reached over
-- SSH, in a container, or in CI there is no `rw` and no browser, so there was no
-- way in at all — the documented answer was to paste a permanent API key into
-- the environment and never rotate it.
--
-- The device authorization grant (RFC 8628) splits the two halves. The machine
-- that wants the session asks here and gets a short code; the human approves
-- that code from a machine that does have a wallet. Nothing secret travels
-- between them: the approver sends a signature over the gateway's own challenge
-- and the waiting machine collects its tokens by presenting the device code it
-- was given.

CREATE TABLE IF NOT EXISTS device_authorizations (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,

    -- SHA-256 of the device code, never the code itself. It is a 256-bit
    -- bearer secret held by the waiting machine, and a table read must not
    -- hand out something that collects a session.
    device_code    TEXT NOT NULL UNIQUE,

    -- The short code the human reads out and types. Stored as it is written,
    -- because the approver looks the row up by it. It is low entropy on
    -- purpose — it is protected by a ten-minute life, one use, and the fact
    -- that approving it still costs the approver a wallet signature.
    user_code      TEXT NOT NULL UNIQUE,

    -- The namespace the waiting machine asked for, or NULL for "whichever the
    -- approver signs in to". Recorded at request time so the approver is shown
    -- what they are approving rather than told afterwards.
    namespace      TEXT,

    -- The wallet that approved, once one has. NULL while pending.
    subject        TEXT,

    approved_at    TIMESTAMP,
    -- Set when the approver refuses. A refusal is a different answer from
    -- "not yet", and the waiting machine should stop rather than poll out its
    -- ten minutes.
    denied_at      TIMESTAMP,
    -- Set when the waiting machine collected its tokens. A device code is
    -- single-use: a second collection with the same code is refused.
    claimed_at     TIMESTAMP,
    -- The last poll, which is what makes the RFC's `slow_down` enforceable
    -- rather than advisory.
    last_polled_at TIMESTAMP,

    expires_at     TIMESTAMP NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- The approver's lookup: user code to pending row.
CREATE INDEX IF NOT EXISTS idx_device_auth_user_code ON device_authorizations(user_code);
-- The sweep that removes what nobody came back for.
CREATE INDEX IF NOT EXISTS idx_device_auth_expires ON device_authorizations(expires_at);
