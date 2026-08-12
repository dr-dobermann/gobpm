-- SRD-091 FR-3 — the adapter's initial objects, mirroring the postgres
-- adapter's 0001_init.sql with the dialect deltas of SRD-091 §3.2.
--
-- Every FOREIGN KEY below is enforced only while PRAGMA foreign_keys is ON,
-- which SQLite leaves OFF by default and applies PER CONNECTION. The adapter
-- sets it in the DSN (Open) or verifies it (New) — see §3.3. Without that,
-- this file's constraints parse and do nothing.

-- The engine-group registry (ADR-033 v.5 §2.8): records may reference
-- established groups only — the FKs below turn the rule into a database
-- guarantee.
CREATE TABLE groups (
    group_name TEXT PRIMARY KEY,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Tenants (ADR-033 v.5 §2.7): keyed per engine group. The default tenant is
-- FLAG-designated, never a reserved id, and the partial unique index makes
-- "one default per group" a database guarantee. name is the human-readable
-- label; real tenant naming/management arrives with the Multi-tenancy ADR.
CREATE TABLE tenants (
    tenant_id    TEXT    NOT NULL,
    engine_group TEXT    NOT NULL REFERENCES groups (group_name),
    name         TEXT    NOT NULL DEFAULT '',
    is_default   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (engine_group, tenant_id)
);

CREATE UNIQUE INDEX tenants_one_default_per_group
    ON tenants (engine_group) WHERE is_default = 1;

-- Instance checkpoints. Deliberately NO CHECK constraint on status: the
-- vocabulary is append-only (SRD-079 added a non-terminal status), and DDL
-- must not reject a status a newer engine writes.
--
-- lease_expiry is TEXT holding RFC 3339 in UTC, not an integer or a local
-- time: ListInFlight COMPARES it, and RFC 3339 in UTC is the encoding whose
-- lexicographic order matches chronological order (§3.2).
CREATE TABLE instances (
    id                TEXT    PRIMARY KEY,
    engine_group      TEXT    NOT NULL REFERENCES groups (group_name),
    tenant_id         TEXT    NOT NULL,
    status            INTEGER NOT NULL,
    payload           BLOB    NOT NULL DEFAULT x'',
    rec_version       INTEGER NOT NULL,
    lease_owner       TEXT    NOT NULL DEFAULT '',
    lease_incarnation INTEGER NOT NULL DEFAULT 0,
    lease_expiry      TEXT    NOT NULL DEFAULT '0001-01-01T00:00:00.000000000Z',
    updated_at        TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    -- the instance -> tenant reference: composite because tenants are keyed
    -- per group, which also guarantees the tenant belongs to the instance's
    -- own engine group.
    FOREIGN KEY (engine_group, tenant_id)
        REFERENCES tenants (engine_group, tenant_id)
);

-- The recovery listing's path (group-scoped claimable scan).
CREATE INDEX instances_recovery_listing
    ON instances (engine_group, status, lease_expiry);
