package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// defaultTenantID is the id the group's flag-designated default tenant row
// is minted under.
const defaultTenantID = "default"

// timeLayout encodes an instant as TEXT so that STRING comparison is
// CHRONOLOGICAL comparison — the property ListInFlight's `lease_expiry <= ?`
// depends on (SRD-091 §3.2).
//
// It is fixed-width and always UTC, and both matter. time.RFC3339Nano trims
// trailing zeros from the fraction, so "…:05.5Z" and "…:05.50Z" denote the
// same instant and compare unequal, while a shorter fraction can sort after a
// longer one. Nine fixed digits remove the ambiguity, and forcing UTC removes
// the offset, which would otherwise sort as text while meaning nothing
// chronologically.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// encodeTime renders an instant in the comparable layout.
func encodeTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// decodeTime parses what encodeTime wrote.
func decodeTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}

	return time.Parse(timeLayout, s)
}

// Save stores the record with compare-and-set semantics: rec.RecVersion must
// equal the stored version (0 creates); the stored version increments on
// success; a mismatch fails with errs.ConcurrentUpdate. The record must carry
// its creator's engine group; an empty Tenant resolves to the group's
// flag-designated default tenant row, created idempotently on first use.
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

// insert creates the record at stored version 1; an existing id means the
// writer lost the CAS race.
func (r *Repo) insert(
	ctx context.Context, rec repository.InstanceRecord, tenant string,
) error {
	res, err := r.db.ExecContext(ctx, r.q.insert,
		rec.ID, rec.Group, tenant, int(rec.Status), rec.Payload,
		rec.Lease.Owner, rec.Lease.Incarnation, encodeTime(rec.Lease.Expiry))
	if err != nil {
		return opErr("Save (create)", rec.ID, err)
	}

	return casOutcome(res, rec.ID, "create")
}

// update advances the record iff the stored version matches; zero rows means
// the record moved (or vanished) under the writer.
func (r *Repo) update(
	ctx context.Context, rec repository.InstanceRecord, tenant string,
) error {
	res, err := r.db.ExecContext(ctx, r.q.update,
		rec.Group, tenant, int(rec.Status), rec.Payload,
		rec.Lease.Owner, rec.Lease.Incarnation, encodeTime(rec.Lease.Expiry),
		rec.ID, rec.RecVersion)
	if err != nil {
		return opErr("Save (update)", rec.ID, err)
	}

	return casOutcome(res, rec.ID, "update")
}

// casOutcome maps "zero rows touched" to the ConcurrentUpdate fencing error
// every adapter reports identically.
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

// resolveTenant returns the tenant row the record lands under, creating it
// idempotently: "" resolves to the group's flag-designated default; an
// explicit id is ensured as a regular (non-default) row.
func (r *Repo) resolveTenant(
	ctx context.Context, group, tenant string,
) (string, error) {
	if tenant == "" {
		return r.ensureDefaultTenant(ctx, group)
	}

	if _, err := r.db.ExecContext(ctx, r.q.ensureTenant,
		tenant, group, tenant); err != nil {
		return "", opErr("ensuring tenant "+tenant, "", err)
	}

	return tenant, nil
}

// ensureDefaultTenant returns the group's flag-designated default tenant id,
// minting the row on first use (group membership is unknown at migration
// time, so the row cannot be pre-created there).
func (r *Repo) ensureDefaultTenant(
	ctx context.Context, group string,
) (string, error) {
	if _, err := r.db.ExecContext(ctx, r.q.mintDefaultTenant,
		defaultTenantID, group); err != nil {
		return "", opErr("ensuring the default tenant", "", err)
	}

	var id string

	err := r.db.QueryRowContext(ctx, r.q.selectDefaultTenant, group).Scan(&id)
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
		expiry string
	)

	rec.ID = id

	err := r.db.QueryRowContext(ctx, r.q.load, id).Scan(
		&rec.Group, &rec.Tenant, &status, &rec.Payload,
		&rec.RecVersion, &rec.Lease.Owner, &rec.Lease.Incarnation, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return repository.InstanceRecord{}, false, nil
	}

	if err != nil {
		return repository.InstanceRecord{}, false, opErr("Load", id, err)
	}

	lease, err := decodeTime(expiry)
	if err != nil {
		return repository.InstanceRecord{}, false,
			opErr("Load (lease expiry)", id, err)
	}

	rec.Lease.Expiry = lease
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

// ListInFlight returns the IDs of the CLAIMABLE in-flight instances of the
// given engine group — not terminal, not suspended, with no live lease at
// now — ordered by id for determinism (ADR-033 §2.8).
func (r *Repo) ListInFlight(
	ctx context.Context, group string, now time.Time,
) ([]string, error) {
	if group == "" {
		return nil, errs.New(
			errs.M("ListInFlight: an empty engine group isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	rows, err := r.db.QueryContext(ctx, r.q.list, group, encodeTime(now))
	if err != nil {
		return nil, opErr("ListInFlight", "", err)
	}

	defer func() {
		//nolint:errcheck // the scan error below is the one that matters
		_ = rows.Close()
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

// RegisterGroup establishes the engine group in the store's registry,
// idempotently (ADR-033 §2.8).
func (r *Repo) RegisterGroup(ctx context.Context, group string) error {
	if group == "" {
		return errs.New(
			errs.M("RegisterGroup: an empty engine group isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, err := r.db.ExecContext(ctx, r.q.registerGroup, group); err != nil {
		return opErr("RegisterGroup", "", err)
	}

	return nil
}

// GroupExists reports whether the group is established in the registry — the
// membership assertion behind "join an existing group only".
func (r *Repo) GroupExists(
	ctx context.Context, group string,
) (bool, error) {
	if group == "" {
		return false, errs.New(
			errs.M("GroupExists: an empty engine group isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	var exists bool

	if err := r.db.QueryRowContext(ctx, r.q.groupExists,
		group).Scan(&exists); err != nil {
		return false, opErr("GroupExists", "", err)
	}

	return exists, nil
}
