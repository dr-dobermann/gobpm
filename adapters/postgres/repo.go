package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// defaultTenantID is the id the adapter mints for a group's
// flag-designated default tenant row on first use. The FLAG is the
// designation (the partial unique index enforces one per group); the
// id is only a convenience name, never reserved — an operator may
// flip is_default to any other row (ADR-033 v.3 §2.7).
const defaultTenantID = "default"

// Save stores the record with compare-and-set semantics (SRD-078
// FR-4): rec.RecVersion must equal the stored version (0 creates); the
// stored version increments on success; a mismatch fails with
// errs.ConcurrentUpdate. The record must carry its creator's engine
// group; an empty Tenant resolves to the group's flag-designated
// default tenant row, created idempotently on first use.
func (r *Repo) Save(ctx context.Context, rec repository.InstanceRecord) error {
	if rec.ID == "" {
		return errs.New(
			errs.M("Save: a record needs an ID"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if rec.Group == "" {
		return errs.New(
			errs.M("Save: a record needs an engine group"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("id", rec.ID))
	}

	registered, err := r.GroupExists(ctx, rec.Group)
	if err != nil {
		return err
	}

	if !registered {
		return errs.New(
			errs.M("Save: engine group %q isn't registered", rec.Group),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("id", rec.ID))
	}

	tenant, err := r.resolveTenant(ctx, rec.Group, rec.Tenant)
	if err != nil {
		return err
	}

	if rec.Payload == nil {
		rec.Payload = []byte{} // a nil slice would land as NULL
	}

	if rec.RecVersion == 0 {
		return r.insert(ctx, rec, tenant)
	}

	return r.update(ctx, rec, tenant)
}

// insert creates the record at stored version 1; an existing id means
// the writer lost the CAS race.
func (r *Repo) insert(
	ctx context.Context, rec repository.InstanceRecord, tenant string,
) error {
	res, err := r.db.ExecContext(ctx, r.q.insert,
		rec.ID, rec.Group, tenant, int(rec.Status), rec.Payload,
		rec.Lease.Owner, rec.Lease.Incarnation, rec.Lease.Expiry)
	if err != nil {
		return opErr("Save (create)", rec.ID, err)
	}

	return casOutcome(res, rec.ID, "create")
}

// update advances the record iff the stored version matches; zero rows
// means the record moved (or vanished) under the writer.
func (r *Repo) update(
	ctx context.Context, rec repository.InstanceRecord, tenant string,
) error {
	res, err := r.db.ExecContext(ctx, r.q.update,
		rec.ID, rec.Group, tenant, int(rec.Status), rec.Payload,
		rec.Lease.Owner, rec.Lease.Incarnation, rec.Lease.Expiry,
		rec.RecVersion)
	if err != nil {
		return opErr("Save (update)", rec.ID, err)
	}

	return casOutcome(res, rec.ID, "update")
}

// casOutcome maps "zero rows touched" to the ConcurrentUpdate fencing
// error every adapter reports identically.
func casOutcome(res sql.Result, id, op string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return opErr("Save ("+op+" outcome)", id, err)
	}

	if n == 0 {
		return errs.New(
			errs.M("Save: the record changed under the writer"),
			errs.C(errorClass, errs.ConcurrentUpdate),
			errs.D("id", id))
	}

	return nil
}

// resolveTenant returns the tenant row the record lands under,
// creating it idempotently: "" resolves to the group's flag-designated
// default; an explicit id is ensured as a regular (non-default) row.
func (r *Repo) resolveTenant(
	ctx context.Context, group, tenant string,
) (string, error) {
	if tenant == "" {
		return r.ensureDefaultTenant(ctx, group)
	}

	if _, err := r.db.ExecContext(ctx, r.q.ensureTenant,
		tenant, group); err != nil {
		return "", opErr("ensuring tenant "+tenant, "", err)
	}

	return tenant, nil
}

// ensureDefaultTenant returns the group's flag-designated default
// tenant id, minting the row on first use (group membership is unknown
// at migration time, so the row cannot be pre-created there).
func (r *Repo) ensureDefaultTenant(
	ctx context.Context, group string,
) (string, error) {
	if _, err := r.db.ExecContext(ctx, r.q.mintDefaultTenant,
		defaultTenantID, group); err != nil {
		return "", opErr("ensuring the default tenant", "", err)
	}

	var id string

	err := r.db.QueryRowContext(ctx, r.q.selectDefaultTenant,
		group).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errs.New(
			errs.M("no default tenant for group %q and the %q id is taken"+
				" by a non-default tenant", group, defaultTenantID),
			errs.C(errorClass, errs.OperationFailed))
	}

	if err != nil {
		return "", opErr("resolving the default tenant", "", err)
	}

	return id, nil
}

// Load returns the record for id; the bool is false when none exists.
func (r *Repo) Load(
	ctx context.Context, id string,
) (repository.InstanceRecord, bool, error) {
	var (
		rec    repository.InstanceRecord
		status int
	)

	rec.ID = id

	err := r.db.QueryRowContext(ctx, r.q.load,
		id).Scan(&rec.Group, &rec.Tenant, &status, &rec.Payload,
		&rec.RecVersion, &rec.Lease.Owner, &rec.Lease.Incarnation,
		&rec.Lease.Expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return repository.InstanceRecord{}, false, nil
	}

	if err != nil {
		return repository.InstanceRecord{}, false, opErr("Load", id, err)
	}

	rec.Status = repository.Status(status)

	return rec, true, nil
}

// Delete removes the record for id (a no-op if it is absent).
func (r *Repo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, r.q.del, id); err != nil {
		return opErr("Delete", id, err)
	}

	return nil
}

// ListInFlight returns the IDs of the CLAIMABLE in-flight instances of
// the given engine group — not terminal, not suspended, with no live
// lease at now — ordered by id for determinism (ADR-033 v.3 §2.8).
func (r *Repo) ListInFlight(
	ctx context.Context, group string, now time.Time,
) ([]string, error) {
	if group == "" {
		return nil, errs.New(
			errs.M("ListInFlight: an engine group is required"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	rows, err := r.db.QueryContext(ctx, r.q.list, group, now)
	if err != nil {
		return nil, opErr("ListInFlight", "", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			r.logger.Warn("ListInFlight: rows close failed", "error", cerr.Error())
		}
	}()

	var ids []string

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, opErr("ListInFlight (scan)", "", err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, opErr("ListInFlight (rows)", "", err)
	}

	return ids, nil
}

// RegisterGroup establishes the engine group in the registry (SRD-078
// FR-1), idempotently.
func (r *Repo) RegisterGroup(ctx context.Context, group string) error {
	if group == "" {
		return errs.New(
			errs.M("RegisterGroup: an engine group is required"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, err := r.db.ExecContext(ctx, r.q.registerGroup,
		group); err != nil {
		return opErr("RegisterGroup "+group, "", err)
	}

	return nil
}

// GroupExists reports whether the group is established in the registry.
func (r *Repo) GroupExists(
	ctx context.Context, group string,
) (bool, error) {
	if group == "" {
		return false, errs.New(
			errs.M("GroupExists: an engine group is required"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	var ok bool

	if err := r.db.QueryRowContext(ctx, r.q.groupExists,
		group).Scan(&ok); err != nil {
		return false, opErr("GroupExists "+group, "", err)
	}

	return ok, nil
}
