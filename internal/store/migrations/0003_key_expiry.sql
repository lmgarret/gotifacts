-- 0003: Optional key expiration. NULL means the key never expires (the default
-- and the behavior of all keys created before this migration).

ALTER TABLE api_keys ADD COLUMN expires_at TEXT;
