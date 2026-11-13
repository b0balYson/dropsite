-- Draft releases: at most one draft per site. A row appears with the first
-- draft and stays: deploy_id NULL means "no draft now, but the stable
-- preview address p-<site id> is allocated", so reconciliation keeps its
-- by-host entry (and the issued certificate stays usable). Purging a
-- deleted site deletes its deploys, which clears deploy_id.
CREATE TABLE site_drafts (
    site_id    TEXT    PRIMARY KEY REFERENCES sites (id),
    deploy_id  TEXT    REFERENCES deploys (id) ON DELETE SET NULL,
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
