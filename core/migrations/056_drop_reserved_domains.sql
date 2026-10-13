-- Migration 056: drop reserved_domains.
--
-- 005_dns_records.sql created the table and seeded api/www/admin/ns1–ns4/mail/
-- cdn/docs/status under orama.network. Nothing has ever SELECTed it. Namespace
-- create already refuses a code denylist; the table's only effect was to
-- suggest those names were reserved while they were not.
--
-- DROP IF EXISTS is idempotent. The table is unused, so dropping it takes
-- nothing with it.
BEGIN;

DROP TABLE IF EXISTS reserved_domains;

COMMIT;
