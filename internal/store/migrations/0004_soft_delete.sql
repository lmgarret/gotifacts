-- 0004_soft_delete: soft-delete support for sites.
-- deleted_at is set when a site is unpublished; a background purge removes rows
-- (and the quarantined files) once the configured TTL has elapsed.

ALTER TABLE sites ADD COLUMN deleted_at TEXT;

CREATE INDEX idx_sites_deleted ON sites (deleted_at);
