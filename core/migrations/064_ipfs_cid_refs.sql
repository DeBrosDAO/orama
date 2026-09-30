-- Which namespaces still reference a CID, cluster-wide.
--
-- IPFS-Cluster keeps ONE pin per CID however many namespaces uploaded the same
-- bytes, so removing that pin is only correct when the last namespace holding
-- the CID lets go. Each namespace records what it holds in its own RQLite
-- (ipfs_content_ownership), and a namespace gateway can only read its own: a
-- count made there never saw another tenant's reference, so the first tenant
-- to unpin shared content deleted the pin, and the blob, out from under the
-- others.
--
-- This table is the count every namespace gateway consults. One row per
-- (cid, namespace, kind):
--   storage    — the namespace pinned it through /v1/storage.
--   deployment — a deployment of the namespace serves it (content or build).
-- The last-reference decision is made against this table only.
--
-- Cluster-registry state (schema_placement.go): a copy in a tenant's RQLite
-- would be a reference count the tenant can rewrite.
--
-- Idempotent: a retried apply changes nothing.

CREATE TABLE IF NOT EXISTS ipfs_cid_refs (
    cid        TEXT NOT NULL,
    namespace  TEXT NOT NULL,
    kind       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (cid, namespace, kind)
);

CREATE INDEX IF NOT EXISTS idx_ipfs_cid_refs_namespace ON ipfs_cid_refs(namespace);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (64);
