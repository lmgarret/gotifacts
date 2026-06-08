-- 0002_mcp: OAuth 2.1 authorization-server state for the MCP connector.
-- All secrets (client secrets, codes, tokens) are stored as SHA-256 hashes only.

CREATE TABLE oauth_clients (
    client_id          TEXT    PRIMARY KEY,
    secret_hash        TEXT,              -- NULL for public (PKCE-only) clients
    client_name        TEXT    NOT NULL DEFAULT '',
    redirect_uris      TEXT    NOT NULL DEFAULT '[]', -- JSON array
    token_auth_method  TEXT    NOT NULL DEFAULT 'none',
    created_at         TEXT    NOT NULL
);

-- Single-use authorization codes (PKCE). Rows are deleted on consumption.
CREATE TABLE oauth_codes (
    code_hash             TEXT    PRIMARY KEY,
    client_id             TEXT    NOT NULL,
    grant_user            TEXT    NOT NULL,
    redirect_uri          TEXT    NOT NULL,
    code_challenge        TEXT    NOT NULL,
    code_challenge_method TEXT    NOT NULL,
    scope                 TEXT    NOT NULL DEFAULT '',
    group_path            TEXT    NOT NULL DEFAULT '',
    expires_at            TEXT    NOT NULL,
    created_at            TEXT    NOT NULL
);

-- Access and refresh tokens. kind is 'access' or 'refresh'.
CREATE TABLE oauth_tokens (
    token_hash  TEXT    PRIMARY KEY,
    kind        TEXT    NOT NULL,
    client_id   TEXT    NOT NULL,
    grant_user  TEXT    NOT NULL,
    scope       TEXT    NOT NULL DEFAULT '',
    group_path  TEXT    NOT NULL DEFAULT '',
    expires_at  TEXT    NOT NULL,
    created_at  TEXT    NOT NULL
);

CREATE INDEX idx_oauth_tokens_kind ON oauth_tokens (kind);
