-- Migration 008: IPFS Namespace Tracking
-- This migration adds namespace isolation for IPFS content by tracking CID ownership.

-- Table: ipfs_content_ownership
-- Tracks which namespace owns each CID uploaded to IPFS.
-- This enables namespace isolation so that:
-- - Namespace-A cannot GET/PIN/UNPIN Namespace-B's content
-- - Same CID can be uploaded by different namespaces (shared content)
CREATE TABLE IF NOT EXISTS ipfs_content_ownership (
    id TEXT PRIMARY KEY,
    cid TEXT NOT NULL,
    namespace TEXT NOT NULL,
    name TEXT,
    size_bytes BIGINT DEFAULT 0,
    is_pinned BOOLEAN DEFAULT FALSE,
    uploaded_at TIMESTAMP NOT NULL,
    uploaded_by TEXT NOT NULL,
    UNIQUE(cid, namespace)
);

-- Index for fast namespace + CID lookup
CREATE INDEX IF NOT EXISTS idx_ipfs_ownership_namespace_cid
    ON ipfs_content_ownership(namespace, cid);

-- Index for fast CID lookup across all namespaces
CREATE INDEX IF NOT EXISTS idx_ipfs_ownership_cid
    ON ipfs_content_ownership(cid);

-- Index for namespace-only queries (list all content for a namespace)
CREATE INDEX IF NOT EXISTS idx_ipfs_ownership_namespace
    ON ipfs_content_ownership(namespace);
