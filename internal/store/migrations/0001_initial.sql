-- Initial schema. All timestamps are Unix milliseconds (UTC).

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    email         TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    password_hash TEXT    NOT NULL DEFAULT '',
    role          TEXT    NOT NULL CHECK (role IN ('admin', 'user')),
    status        TEXT    NOT NULL CHECK (status IN ('invited', 'active', 'blocked')),
    lang          TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id),
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL,
    hash         BLOB    NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    expires_at   INTEGER,
    revoked_at   INTEGER
) STRICT;
CREATE INDEX tokens_user ON tokens (user_id);

CREATE TABLE sessions (
    id           BLOB    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id),
    csrf_secret  BLOB    NOT NULL,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    ip           TEXT    NOT NULL DEFAULT '',
    user_agent   TEXT    NOT NULL DEFAULT ''
) STRICT, WITHOUT ROWID;
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE invites (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id),
    hash       BLOB    NOT NULL UNIQUE,
    created_by INTEGER NOT NULL REFERENCES users (id),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    revoked_at INTEGER
) STRICT;
CREATE INDEX invites_user ON invites (user_id);

-- A site row is never deleted: after purge it stays as a tombstone so its ID
-- (and therefore its default address) is never issued again.
CREATE TABLE sites (
    id                TEXT    PRIMARY KEY,
    owner_id          INTEGER NOT NULL REFERENCES users (id),
    title             TEXT    NOT NULL,
    current_deploy_id TEXT,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    deleted_at        INTEGER,
    purged_at         INTEGER
) STRICT, WITHOUT ROWID;
CREATE INDEX sites_owner ON sites (owner_id) WHERE deleted_at IS NULL;
CREATE INDEX sites_deleted ON sites (deleted_at) WHERE deleted_at IS NOT NULL AND purged_at IS NULL;

-- Only active hostnames live here; a soft-deleted site loses all its rows at once.
CREATE TABLE hostnames (
    hostname   TEXT    PRIMARY KEY,
    site_id    TEXT    NOT NULL REFERENCES sites (id),
    kind       TEXT    NOT NULL CHECK (kind IN ('default', 'slug', 'apex', 'external')),
    verified   INTEGER NOT NULL DEFAULT 1 CHECK (verified IN (0, 1)),
    created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX hostnames_site ON hostnames (site_id);
CREATE UNIQUE INDEX hostnames_one_slug ON hostnames (site_id) WHERE kind = 'slug';
CREATE UNIQUE INDEX hostnames_one_default ON hostnames (site_id) WHERE kind = 'default';
CREATE UNIQUE INDEX hostnames_one_apex ON hostnames (kind) WHERE kind = 'apex';

CREATE TABLE deploys (
    id         TEXT    PRIMARY KEY,
    site_id    TEXT    NOT NULL REFERENCES sites (id),
    created_by INTEGER NOT NULL REFERENCES users (id),
    source     TEXT    NOT NULL CHECK (source IN ('ui', 'api', 'cli', 'mcp')),
    size_bytes INTEGER NOT NULL,
    file_count INTEGER NOT NULL,
    status     TEXT    NOT NULL CHECK (status IN ('ready', 'failed', 'pruned')),
    created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX deploys_site ON deploys (site_id, created_at);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY,
    actor_id   INTEGER REFERENCES users (id),
    action     TEXT    NOT NULL,
    target     TEXT    NOT NULL DEFAULT '',
    meta_json  TEXT    NOT NULL DEFAULT '{}',
    ip         TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX audit_created ON audit_log (created_at);
CREATE INDEX audit_actor ON audit_log (actor_id, created_at);
CREATE INDEX audit_action ON audit_log (action, created_at);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT, WITHOUT ROWID;
