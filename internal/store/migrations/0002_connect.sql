-- Connect codes and the OAuth authorization server. Timestamps are Unix ms.

CREATE TABLE connect_codes (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id),
    hash       BLOB    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
) STRICT;
CREATE INDEX connect_codes_expires ON connect_codes (expires_at);

-- Dynamically registered (RFC 7591) public clients. Clients identified by a
-- metadata document URL (CIMD) are not stored here.
CREATE TABLE oauth_clients (
    id            TEXT    PRIMARY KEY,
    name          TEXT    NOT NULL,
    redirect_uris TEXT    NOT NULL, -- JSON array
    created_at    INTEGER NOT NULL,
    created_ip    TEXT    NOT NULL DEFAULT ''
) STRICT, WITHOUT ROWID;

-- One connected application of a user; revoking it kills every token.
CREATE TABLE oauth_grants (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users (id),
    client_id     TEXT    NOT NULL,
    client_name   TEXT    NOT NULL,
    redirect_host TEXT    NOT NULL,
    scope         TEXT    NOT NULL,
    resource      TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    last_used_at  INTEGER,
    revoked_at    INTEGER
) STRICT;
CREATE INDEX oauth_grants_user ON oauth_grants (user_id, client_id);

CREATE TABLE oauth_codes (
    id           INTEGER PRIMARY KEY,
    hash         BLOB    NOT NULL UNIQUE,
    user_id      INTEGER NOT NULL REFERENCES users (id),
    client_id    TEXT    NOT NULL,
    client_name  TEXT    NOT NULL,
    redirect_uri TEXT    NOT NULL,
    challenge    TEXT    NOT NULL,
    resource     TEXT    NOT NULL,
    scope        TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    used_at      INTEGER,
    grant_id     INTEGER REFERENCES oauth_grants (id)
) STRICT;
CREATE INDEX oauth_codes_expires ON oauth_codes (expires_at);

CREATE TABLE oauth_tokens (
    id         INTEGER PRIMARY KEY,
    hash       BLOB    NOT NULL UNIQUE,
    kind       TEXT    NOT NULL CHECK (kind IN ('access', 'refresh')),
    grant_id   INTEGER NOT NULL REFERENCES oauth_grants (id),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    revoked_at INTEGER
) STRICT;
CREATE INDEX oauth_tokens_grant ON oauth_tokens (grant_id);
CREATE INDEX oauth_tokens_expires ON oauth_tokens (expires_at);
