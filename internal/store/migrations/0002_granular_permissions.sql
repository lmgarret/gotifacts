-- 0002: Replace the coarse scope/group_restriction model with GitHub-style
-- per-key grants (a group subtree + a set of capabilities) plus an admin flag.
--
-- Backward compatibility: API tokens are stored only as hashes and are never
-- regenerated. This migration backfills equivalent grants from each existing
-- row's scope/group_restriction, so every issued token keeps exactly the access
-- it had before.

ALTER TABLE api_keys ADD COLUMN admin INTEGER NOT NULL DEFAULT 0;

CREATE TABLE api_key_grants (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    key_id      INTEGER NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_path  TEXT    NOT NULL DEFAULT '',
    permissions TEXT    NOT NULL
);

CREATE INDEX idx_api_key_grants_key ON api_key_grants(key_id);

-- Old admin keys become superusers.
UPDATE api_keys SET admin = 1 WHERE scope = 'admin';

-- Old publish keys get a single publish grant preserving their group subtree.
INSERT INTO api_key_grants (key_id, group_path, permissions)
    SELECT id, COALESCE(group_restriction, ''), 'publish'
    FROM api_keys WHERE scope = 'publish';

-- Legacy columns are superseded; neither participates in an index or constraint.
ALTER TABLE api_keys DROP COLUMN scope;
ALTER TABLE api_keys DROP COLUMN group_restriction;
