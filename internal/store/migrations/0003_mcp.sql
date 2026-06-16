-- 0003_mcp: OAuth 2.1 authorization-server state for the MCP connector.
-- All secrets (client secrets, codes, tokens) are stored as SHA-256 hashes only.
--
-- Authorization codes and tokens carry a JSON-encoded set of grants matching the
-- api_key_grants model (kind/target/permissions), so an MCP connector is scoped
-- exactly like a scoped API key. Tokens issued from one consent share a conn_id
-- so a "connection" can be listed and revoked as a unit.

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
    grants                TEXT    NOT NULL DEFAULT '[]', -- JSON []store.Grant
    expires_at            TEXT    NOT NULL,
    created_at            TEXT    NOT NULL
);

-- Access and refresh tokens. kind is 'access' or 'refresh'. conn_id groups all
-- tokens issued from a single consent (survives refresh rotation), so a
-- connection can be listed and revoked as a unit.
CREATE TABLE oauth_tokens (
    token_hash   TEXT    PRIMARY KEY,
    conn_id      TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    client_id    TEXT    NOT NULL,
    grant_user   TEXT    NOT NULL,
    grants       TEXT    NOT NULL DEFAULT '[]', -- JSON []store.Grant
    expires_at   TEXT    NOT NULL,
    created_at   TEXT    NOT NULL,
    last_used_at TEXT
);

CREATE INDEX idx_oauth_tokens_kind ON oauth_tokens (kind);
CREATE INDEX idx_oauth_tokens_conn ON oauth_tokens (conn_id);
