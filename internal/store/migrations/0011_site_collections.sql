-- Site collections: small JSON data of a site that pages read at
-- /_dropsite/data/<name>. All timestamps are Unix milliseconds (UTC).

-- schema_json is NULL when the collection has no schema. data_version grows
-- with every change of the collection's documents (it is the public ETag).
CREATE TABLE site_collections (
    site_id      TEXT    NOT NULL REFERENCES sites (id),
    name         TEXT    NOT NULL,
    title        TEXT    NOT NULL DEFAULT '',
    mode         TEXT    NOT NULL CHECK (mode IN ('public', 'moderated')),
    schema_json  TEXT,
    data_version INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (site_id, name)
) STRICT, WITHOUT ROWID;

CREATE TABLE site_documents (
    site_id    TEXT    NOT NULL,
    collection TEXT    NOT NULL,
    id         TEXT    NOT NULL,
    data_json  TEXT    NOT NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    status     TEXT    NOT NULL CHECK (status IN ('approved', 'pending')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (site_id, collection, id),
    FOREIGN KEY (site_id, collection) REFERENCES site_collections (site_id, name) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
CREATE INDEX site_documents_position ON site_documents (site_id, collection, status, position, created_at);
CREATE INDEX site_documents_created ON site_documents (site_id, collection, status, created_at);
