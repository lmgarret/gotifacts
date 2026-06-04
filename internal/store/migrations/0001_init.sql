-- 0001_init: initial registry schema.
CREATE TABLE sites (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    group_path  TEXT    NOT NULL DEFAULT '',
    slug        TEXT    NOT NULL,
    title       TEXT    NOT NULL DEFAULT '',
    description TEXT    NOT NULL DEFAULT '',
    date        TEXT    NOT NULL DEFAULT '',
    tags        TEXT    NOT NULL DEFAULT '[]',
    repo        TEXT    NOT NULL DEFAULT '',
    preview     TEXT    NOT NULL DEFAULT '',
    hidden      INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL,
    UNIQUE (group_path, slug)
);

CREATE INDEX idx_sites_group_path ON sites (group_path);
CREATE INDEX idx_sites_hidden     ON sites (hidden);
CREATE INDEX idx_sites_date       ON sites (date);

CREATE TABLE api_keys (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    name              TEXT    NOT NULL,
    key_hash          TEXT    NOT NULL UNIQUE,
    scope             TEXT    NOT NULL,
    group_restriction TEXT,
    created_at        TEXT    NOT NULL,
    last_used_at      TEXT
);

CREATE INDEX idx_api_keys_hash ON api_keys (key_hash);
