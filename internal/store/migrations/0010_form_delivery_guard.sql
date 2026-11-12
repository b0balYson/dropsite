-- Form delivery hardening: a per-channel circuit breaker and a 'cancelled'
-- delivery status for channels that were turned off or sites that were
-- deleted while deliveries were queued.

-- consecutive_failures counts failed attempts in a row; paused_at is set
-- when the breaker opens and cleared when the owner resumes the channel.
ALTER TABLE form_channels ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE form_channels ADD COLUMN paused_at INTEGER;

-- SQLite cannot change a CHECK constraint in place: rebuild the queue table.
CREATE TABLE form_deliveries_new (
    id              INTEGER PRIMARY KEY,
    submission_id   INTEGER NOT NULL REFERENCES form_submissions (id) ON DELETE CASCADE,
    channel_id      INTEGER NOT NULL REFERENCES form_channels (id) ON DELETE CASCADE,
    status          TEXT    NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'cancelled')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_error      TEXT    NOT NULL DEFAULT '',
    sent_at         INTEGER
) STRICT;
INSERT INTO form_deliveries_new (id, submission_id, channel_id, status, attempts, next_attempt_at, last_error, sent_at)
    SELECT id, submission_id, channel_id, status, attempts, next_attempt_at, last_error, sent_at FROM form_deliveries;
DROP TABLE form_deliveries;
ALTER TABLE form_deliveries_new RENAME TO form_deliveries;
CREATE INDEX form_deliveries_due ON form_deliveries (status, next_attempt_at);
CREATE INDEX form_deliveries_submission ON form_deliveries (submission_id);
CREATE INDEX form_deliveries_channel ON form_deliveries (channel_id);
