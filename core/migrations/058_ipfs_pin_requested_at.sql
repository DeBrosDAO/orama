-- When a namespace last uploaded or pinned a CID (bugboard #414).
--
-- A download of a CID this node does not hold asks the cluster's pinset, and a
-- CID missing from it is answered NOT_FOUND at once. The pinset is CRDT state,
-- so for a moment after a pin another node may not list it yet; within a short
-- window after the pin was requested that NOT_FOUND is marked retryable.
--
-- uploaded_at cannot carry this. The ownership row is kept when content is
-- unpinned and is not rewritten when the same bytes are uploaded again, so
-- uploaded_at is the first upload, and a re-upload of identical content would
-- have been answered as gone while its pin propagated.

ALTER TABLE ipfs_content_ownership ADD COLUMN pin_requested_at TIMESTAMP;

UPDATE ipfs_content_ownership SET pin_requested_at = uploaded_at WHERE pin_requested_at IS NULL;

INSERT OR IGNORE INTO schema_migrations(version) VALUES (58);
