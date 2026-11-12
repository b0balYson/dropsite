-- Visit statistics from the sites access log and analytics counter IDs.
-- Days are UTC dates 'YYYY-MM-DD'; they compare correctly as text.

CREATE TABLE stats_daily (
    site_id  TEXT    NOT NULL,
    day      TEXT    NOT NULL,
    views    INTEGER NOT NULL DEFAULT 0,
    visitors INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, day)
) STRICT, WITHOUT ROWID;
CREATE INDEX stats_daily_day ON stats_daily (day);

-- path '*' collects views beyond the per-day row limit.
CREATE TABLE stats_pages (
    site_id TEXT    NOT NULL,
    day     TEXT    NOT NULL,
    path    TEXT    NOT NULL,
    views   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, day, path)
) STRICT, WITHOUT ROWID;
CREATE INDEX stats_pages_day ON stats_pages (day);

-- host '' is a direct visit, '*' collects views beyond the per-day limit.
CREATE TABLE stats_referrers (
    site_id TEXT    NOT NULL,
    day     TEXT    NOT NULL,
    host    TEXT    NOT NULL,
    views   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, day, host)
) STRICT, WITHOUT ROWID;
CREATE INDEX stats_referrers_day ON stats_referrers (day);

-- Visitor hashes and salts exist only for the current day: both are deleted
-- by the first collection pass after the day ends.
CREATE TABLE stats_visitors (
    site_id TEXT NOT NULL,
    day     TEXT NOT NULL,
    hash    BLOB NOT NULL,
    PRIMARY KEY (site_id, day, hash)
) STRICT, WITHOUT ROWID;

CREATE TABLE stats_salts (
    day  TEXT PRIMARY KEY,
    salt BLOB NOT NULL
) STRICT, WITHOUT ROWID;

-- Read position in the access log; one row.
CREATE TABLE stats_cursor (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    inode       INTEGER NOT NULL,
    byte_offset INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;

CREATE TABLE site_counters (
    site_id    TEXT    PRIMARY KEY REFERENCES sites (id),
    metrika_id TEXT    NOT NULL DEFAULT '',
    ga4_id     TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
