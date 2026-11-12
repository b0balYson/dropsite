-- Publication pipeline: per-site publish settings. A site without a row uses
-- the defaults (optimization on).
CREATE TABLE site_publish_settings (
    site_id  TEXT    PRIMARY KEY REFERENCES sites (id),
    optimize INTEGER NOT NULL DEFAULT 1 CHECK (optimize IN (0, 1))
) STRICT, WITHOUT ROWID;
