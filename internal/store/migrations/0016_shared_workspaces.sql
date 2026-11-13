-- Shared workspaces: equal members have owner rights on the workspace's
-- sites. sites.owner_id stays the creator of a site (limits count against
-- it); sites.workspace_id is NULL for a personal site.
CREATE TABLE workspaces (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    created_by INTEGER REFERENCES users (id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE workspace_members (
    workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      INTEGER NOT NULL REFERENCES users (id),
    added_by     INTEGER REFERENCES users (id),
    created_at   INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, user_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX workspace_members_user ON workspace_members (user_id, workspace_id);

ALTER TABLE sites ADD COLUMN workspace_id INTEGER REFERENCES workspaces (id);
CREATE INDEX sites_workspace ON sites (workspace_id) WHERE deleted_at IS NULL;
