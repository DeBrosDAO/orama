CREATE TABLE IF NOT EXISTS invite_tokens (
    token TEXT PRIMARY KEY,
    created_by TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    used_at DATETIME,
    used_by_ip TEXT
);
