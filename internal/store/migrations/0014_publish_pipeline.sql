-- Publication pipeline: per-site publish settings. A site without a row uses
-- the defaults (optimization on).
CREATE TABLE site_publish_settings (
    site_id  TEXT    PRIMARY KEY REFERENCES sites (id),
    optimize INTEGER NOT NULL DEFAULT 1 CHECK (optimize IN (0, 1))
) STRICT, WITHOUT ROWID;

-- Quality report of a release (JSON, see internal/quality), written after
-- the release is committed. No foreign key to deploys: deploy rows of a
-- purged site are deleted, and reports go with them (deletePublishData).
CREATE TABLE deploy_reports (
    site_id    TEXT    NOT NULL REFERENCES sites (id),
    deploy_id  TEXT    NOT NULL,
    report     TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (site_id, deploy_id)
) STRICT, WITHOUT ROWID;
