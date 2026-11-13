-- Abuse protection. Proxy endpoints get a class with built-in policies,
-- an optional declarative request template (JSON) and owner overrides of
-- the class limits (0: the class default). Existing endpoints keep their
-- old behaviour as "transparent".
ALTER TABLE proxy_endpoints ADD COLUMN class TEXT NOT NULL DEFAULT 'transparent'
    CHECK (class IN ('read', 'ai', 'send', 'transparent'));
ALTER TABLE proxy_endpoints ADD COLUMN template TEXT NOT NULL DEFAULT '';
ALTER TABLE proxy_endpoints ADD COLUMN rate_per_minute INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_endpoints ADD COLUMN daily_budget INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_endpoints ADD COLUMN session_quota INTEGER NOT NULL DEFAULT 0;

-- Calls per endpoint and UTC day (YYYY-MM-DD) against the daily budget;
-- notified is set once the owner was told the budget ran out that day.
CREATE TABLE proxy_daily_usage (
    endpoint_id INTEGER NOT NULL REFERENCES proxy_endpoints (id) ON DELETE CASCADE,
    day         TEXT    NOT NULL,
    calls       INTEGER NOT NULL DEFAULT 0,
    notified    INTEGER NOT NULL DEFAULT 0 CHECK (notified IN (0, 1)),
    PRIMARY KEY (endpoint_id, day)
) STRICT, WITHOUT ROWID;

-- IP bans. key is an IPv4 address or an IPv6 /64 network; addresses is a
-- JSON array of the exact client addresses that got a marker file under
-- /data/bans/ (Caddy compares exact addresses). A row outlives its ban by
-- a day so a repeat offence gets the next, longer ban.
CREATE TABLE ip_bans (
    key        TEXT    PRIMARY KEY,
    level      INTEGER NOT NULL,
    reason     TEXT    NOT NULL,
    addresses  TEXT    NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX ip_bans_expires ON ip_bans (expires_at);
