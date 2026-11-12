-- External domains: an administrator approves (verified = 1) or rejects them.
-- Status is derived: verified = 1 → active; verified = 0 and reviewed_at IS
-- NULL → pending; verified = 0 and reviewed_at set → rejected. Existing rows
-- (default, slug, apex) are verified and keep reviewed_at NULL.

ALTER TABLE hostnames ADD COLUMN reject_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE hostnames ADD COLUMN reviewed_at INTEGER;
ALTER TABLE hostnames ADD COLUMN reviewed_by INTEGER REFERENCES users (id);

CREATE INDEX hostnames_external ON hostnames (verified, created_at) WHERE kind = 'external';
