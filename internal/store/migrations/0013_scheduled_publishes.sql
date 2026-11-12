-- Scheduled publishes: a ready release of a site goes live at publish_at.
-- The worker in control switches the site like a rollback and records the
-- outcome; rows of deleted sites are cancelled by the worker.
CREATE TABLE scheduled_publishes (
    id          INTEGER PRIMARY KEY,
    site_id     TEXT    NOT NULL REFERENCES sites (id),
    -- Purging a deleted site deletes its deploys and with them its rows.
    deploy_id   TEXT    NOT NULL REFERENCES deploys (id) ON DELETE CASCADE,
    created_by  INTEGER REFERENCES users (id) ON DELETE SET NULL,
    publish_at  INTEGER NOT NULL,
    status      TEXT    NOT NULL CHECK (status IN ('pending', 'done', 'cancelled', 'failed')),
    error       TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    finished_at INTEGER
) STRICT;
CREATE INDEX scheduled_publishes_due ON scheduled_publishes (status, publish_at);
CREATE INDEX scheduled_publishes_site ON scheduled_publishes (site_id, status);
-- A release waits for at most one publish.
CREATE UNIQUE INDEX scheduled_publishes_pending_deploy ON scheduled_publishes (deploy_id) WHERE status = 'pending';
