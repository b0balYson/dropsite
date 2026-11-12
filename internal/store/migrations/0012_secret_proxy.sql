-- Secret proxy: named endpoints of a site that forward visitor requests to
-- an upstream API and add a stored secret. allowed_hosts, methods and paths
-- are JSON arrays of strings; secret_enc is sealed with the installation
-- key ('' when no secret is set). The usage columns are plain counters.
-- All timestamps are Unix milliseconds (UTC).

CREATE TABLE proxy_endpoints (
    id                INTEGER PRIMARY KEY,
    site_id           TEXT    NOT NULL REFERENCES sites (id),
    name              TEXT    NOT NULL,
    upstream_url      TEXT    NOT NULL,
    allowed_hosts     TEXT    NOT NULL,
    methods           TEXT    NOT NULL,
    paths             TEXT    NOT NULL,
    auth_kind         TEXT    NOT NULL CHECK (auth_kind IN ('header', 'query', 'bearer', 'path')),
    auth_name         TEXT    NOT NULL DEFAULT '',
    enabled           INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    secret_enc        TEXT    NOT NULL DEFAULT '',
    secret_updated_at INTEGER,
    requests          INTEGER NOT NULL DEFAULT 0,
    errors            INTEGER NOT NULL DEFAULT 0,
    rejected          INTEGER NOT NULL DEFAULT 0,
    last_used_at      INTEGER,
    last_status       INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    UNIQUE (site_id, name)
) STRICT;
