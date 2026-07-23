-- 0006_add_favicon: cache the detected favicon for each site.
-- favicon holds the raw icon bytes (empty/NULL when the site has none);
-- favicon_type is its MIME content-type, used when serving. Both are derived
-- server-side from the published index.html, never supplied by the client.
-- The blob is deliberately excluded from list queries so it never bloats scans.

ALTER TABLE sites ADD COLUMN favicon BLOB;

ALTER TABLE sites ADD COLUMN favicon_type TEXT NOT NULL DEFAULT '';
