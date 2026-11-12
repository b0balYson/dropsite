-- Site access controls (noindex, password protection, expiry), per-user
-- limit overrides and installation secrets. Times are Unix milliseconds.

-- access_password_hash is an argon2id hash; '' means the site is open.
-- access_version grows on every password change or removal so that visitor
-- cookies signed for an earlier version stop matching.
ALTER TABLE sites ADD COLUMN noindex INTEGER NOT NULL DEFAULT 0 CHECK (noindex IN (0, 1));
ALTER TABLE sites ADD COLUMN access_password_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN access_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN expires_at INTEGER;
CREATE INDEX sites_expires ON sites (expires_at) WHERE expires_at IS NOT NULL AND deleted_at IS NULL;

-- NULL means "not overridden": the global effective value applies.
CREATE TABLE user_limits (
    user_id            INTEGER PRIMARY KEY REFERENCES users (id),
    max_site_bytes     INTEGER CHECK (max_site_bytes > 0),
    max_file_bytes     INTEGER CHECK (max_file_bytes > 0),
    max_files          INTEGER CHECK (max_files > 0),
    max_sites_per_user INTEGER CHECK (max_sites_per_user > 0),
    keep_releases      INTEGER CHECK (keep_releases >= 2),
    updated_at         INTEGER NOT NULL
) STRICT;

-- Random keys generated once per installation (for example the HMAC key of
-- visitor cookies). They live in the database so a backup restores them.
CREATE TABLE instance_secrets (
    name       TEXT    PRIMARY KEY,
    value      BLOB    NOT NULL,
    created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
