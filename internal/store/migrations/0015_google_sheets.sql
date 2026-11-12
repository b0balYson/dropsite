-- Google Sheets: a 'sheets' form delivery channel and links between site
-- collections and sheets. All timestamps are Unix milliseconds (UTC).

-- SQLite cannot change a CHECK constraint in place. Dropping form_channels
-- while form_deliveries references it would cascade-delete the queue
-- (foreign keys are on), so both tables are copied first, the old ones are
-- dropped child first, and the copies are renamed; renaming rewrites the
-- foreign key of form_deliveries to the final name.
CREATE TABLE form_channels_new (
    id                   INTEGER PRIMARY KEY,
    site_id              TEXT    NOT NULL REFERENCES sites (id),
    kind                 TEXT    NOT NULL CHECK (kind IN ('telegram', 'email', 'webhook', 'sheets')),
    target               TEXT    NOT NULL DEFAULT '',
    secret_enc           TEXT    NOT NULL DEFAULT '',
    status               TEXT    NOT NULL CHECK (status IN ('pending', 'active')),
    enabled              INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    verify_hash          BLOB,
    verify_expires_at    INTEGER,
    verify_attempts      INTEGER NOT NULL DEFAULT 0,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    paused_at            INTEGER
) STRICT;
INSERT INTO form_channels_new (id, site_id, kind, target, secret_enc, status, enabled, verify_hash,
        verify_expires_at, verify_attempts, created_at, updated_at, consecutive_failures, paused_at)
    SELECT id, site_id, kind, target, secret_enc, status, enabled, verify_hash,
        verify_expires_at, verify_attempts, created_at, updated_at, consecutive_failures, paused_at
    FROM form_channels;

CREATE TABLE form_deliveries_new (
    id              INTEGER PRIMARY KEY,
    submission_id   INTEGER NOT NULL REFERENCES form_submissions (id) ON DELETE CASCADE,
    channel_id      INTEGER NOT NULL REFERENCES form_channels_new (id) ON DELETE CASCADE,
    status          TEXT    NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'cancelled')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_error      TEXT    NOT NULL DEFAULT '',
    sent_at         INTEGER
) STRICT;
INSERT INTO form_deliveries_new (id, submission_id, channel_id, status, attempts, next_attempt_at, last_error, sent_at)
    SELECT id, submission_id, channel_id, status, attempts, next_attempt_at, last_error, sent_at FROM form_deliveries;

DROP TABLE form_deliveries;
DROP TABLE form_channels;
ALTER TABLE form_channels_new RENAME TO form_channels;
ALTER TABLE form_deliveries_new RENAME TO form_deliveries;
CREATE INDEX form_channels_site ON form_channels (site_id);
CREATE INDEX form_deliveries_due ON form_deliveries (status, next_attempt_at);
CREATE INDEX form_deliveries_submission ON form_deliveries (submission_id);
CREATE INDEX form_deliveries_channel ON form_deliveries (channel_id);

-- A collection linked to a sheet; the sheet is the source of truth for the
-- collection's documents. sheet_gid is NULL for the first sheet;
-- interval_minutes 0 means manual synchronization only. last_errors is a
-- JSON array of {"row", "field", "message"}.
CREATE TABLE collection_sheets (
    site_id          TEXT    NOT NULL,
    collection       TEXT    NOT NULL,
    spreadsheet_id   TEXT    NOT NULL,
    sheet_gid        INTEGER,
    sheet_title      TEXT    NOT NULL DEFAULT '',
    interval_minutes INTEGER NOT NULL DEFAULT 0,
    next_sync_at     INTEGER,
    last_sync_at     INTEGER,
    last_status      TEXT    NOT NULL DEFAULT '' CHECK (last_status IN ('', 'ok', 'failed')),
    last_trigger     TEXT    NOT NULL DEFAULT '',
    last_rows        INTEGER NOT NULL DEFAULT 0,
    last_message     TEXT    NOT NULL DEFAULT '',
    last_errors      TEXT    NOT NULL DEFAULT '[]',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    PRIMARY KEY (site_id, collection),
    FOREIGN KEY (site_id, collection) REFERENCES site_collections (site_id, name) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
CREATE INDEX collection_sheets_due ON collection_sheets (next_sync_at) WHERE interval_minutes > 0;
