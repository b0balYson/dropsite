-- Site forms: delivery channels, stored submissions and the delivery queue.
-- All timestamps are Unix milliseconds (UTC).

CREATE TABLE form_channels (
    id                INTEGER PRIMARY KEY,
    site_id           TEXT    NOT NULL REFERENCES sites (id),
    kind              TEXT    NOT NULL CHECK (kind IN ('telegram', 'email', 'webhook')),
    target            TEXT    NOT NULL DEFAULT '',
    secret_enc        TEXT    NOT NULL DEFAULT '',
    status            TEXT    NOT NULL CHECK (status IN ('pending', 'active')),
    enabled           INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    verify_hash       BLOB,
    verify_expires_at INTEGER,
    verify_attempts   INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
) STRICT;
CREATE INDEX form_channels_site ON form_channels (site_id);

CREATE TABLE form_site_settings (
    site_id        TEXT    PRIMARY KEY REFERENCES sites (id),
    require_altcha INTEGER NOT NULL DEFAULT 0 CHECK (require_altcha IN (0, 1))
) STRICT, WITHOUT ROWID;

-- fields_json is an ordered array of {"name", "value"} objects.
CREATE TABLE form_submissions (
    id          INTEGER PRIMARY KEY,
    site_id     TEXT    NOT NULL REFERENCES sites (id),
    host        TEXT    NOT NULL,
    form        TEXT    NOT NULL,
    fields_json TEXT    NOT NULL,
    created_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX form_submissions_site ON form_submissions (site_id, created_at);
CREATE INDEX form_submissions_created ON form_submissions (created_at);

CREATE TABLE form_deliveries (
    id              INTEGER PRIMARY KEY,
    submission_id   INTEGER NOT NULL REFERENCES form_submissions (id) ON DELETE CASCADE,
    channel_id      INTEGER NOT NULL REFERENCES form_channels (id) ON DELETE CASCADE,
    status          TEXT    NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_error      TEXT    NOT NULL DEFAULT '',
    sent_at         INTEGER
) STRICT;
CREATE INDEX form_deliveries_due ON form_deliveries (status, next_attempt_at);
CREATE INDEX form_deliveries_submission ON form_deliveries (submission_id);
CREATE INDEX form_deliveries_channel ON form_deliveries (channel_id);

-- Accepted Altcha solutions, kept until their challenge expires so that one
-- solution cannot be replayed.
CREATE TABLE form_altcha_used (
    signature  TEXT    PRIMARY KEY,
    expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX form_altcha_used_expires ON form_altcha_used (expires_at);
