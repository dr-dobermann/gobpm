-- SRD-078 FR-5 — the adapter's initial objects. Executed with
-- search_path set to the adapter's schema; every name is unqualified
-- on purpose.

-- The engine-group registry (FR-1, ADR-033 v.3 §2.8): records may
-- reference established groups only — the FKs below turn the rule
-- into a database guarantee.
CREATE TABLE groups (
    group_name text        PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Tenants (ADR-033 v.3 §2.7): keyed per engine group. The default
-- tenant is FLAG-designated, never a reserved id, and the partial
-- unique index makes "one default per group" a database guarantee.
-- name is the human-readable label; real tenant naming/management
-- arrives with the Multi-tenancy ADR.
CREATE TABLE tenants (
    tenant_id    text        NOT NULL,
    engine_group text        NOT NULL REFERENCES groups (group_name),
    name         text        NOT NULL DEFAULT '',
    is_default   boolean     NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (engine_group, tenant_id)
);

CREATE UNIQUE INDEX tenants_one_default_per_group
    ON tenants (engine_group) WHERE is_default;

-- Instance checkpoints. Deliberately NO CHECK constraint on status:
-- the vocabulary is append-only (SRD-079 adds a non-terminal status),
-- and DDL must not reject a status a newer engine writes.
CREATE TABLE instances (
    id                text        PRIMARY KEY,
    engine_group      text        NOT NULL REFERENCES groups (group_name),
    tenant_id         text        NOT NULL,
    -- the instance -> tenant reference: composite because tenants are
    -- keyed per group, which also guarantees the tenant belongs to the
    -- instance's own engine group.
    FOREIGN KEY (engine_group, tenant_id)
        REFERENCES tenants (engine_group, tenant_id),
    status            integer     NOT NULL,
    payload           bytea       NOT NULL DEFAULT ''::bytea,
    rec_version       bigint      NOT NULL,
    lease_owner       text        NOT NULL DEFAULT '',
    lease_incarnation bigint      NOT NULL DEFAULT 0,
    lease_expiry      timestamptz NOT NULL DEFAULT 'epoch',
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- The recovery listing's path (group-scoped claimable scan).
CREATE INDEX instances_recovery_listing
    ON instances (engine_group, status, lease_expiry);
