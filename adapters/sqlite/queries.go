package sqlite

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// queries holds the adapter's SQL. SQLite has no schemas, so unlike the
// postgres adapter nothing here is namespaced and the set is built once.
type queries struct {
	insert              string
	update              string
	load                string
	del                 string
	list                string
	registerGroup       string
	groupExists         string
	ensureTenant        string
	mintDefaultTenant   string
	selectDefaultTenant string
}

// claimExcluded are the statuses ListInFlight must never return: a settled
// instance has nothing to recover, and a suspended one is withheld from
// recovery by an operator's decision (ADR-033 §2.6).
var claimExcluded = fmt.Sprintf("(%d, %d, %d)",
	repository.StatusCompleted,
	repository.StatusTerminated,
	repository.StatusSuspended)

// buildQueries assembles the statement set.
//
// Placeholders are "?" rather than $N: modernc.org/sqlite binds positionally
// and the arguments are passed in order.
func buildQueries() queries {
	return queries{
		insert: "INSERT INTO instances" +
			" (id, engine_group, tenant_id, status, payload, rec_version," +
			" lease_owner, lease_incarnation, lease_expiry)" +
			" VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)" +
			" ON CONFLICT (id) DO NOTHING",
		update: "UPDATE instances" +
			" SET engine_group = ?, tenant_id = ?, status = ?," +
			" payload = ?, rec_version = rec_version + 1," +
			" lease_owner = ?, lease_incarnation = ?, lease_expiry = ?," +
			" updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')" +
			" WHERE id = ? AND rec_version = ?",
		load: "SELECT engine_group, tenant_id, status, payload," +
			" rec_version, lease_owner, lease_incarnation, lease_expiry" +
			" FROM instances WHERE id = ?",
		del: "DELETE FROM instances WHERE id = ?",
		// lease_expiry is RFC 3339 in UTC, so this string comparison is a
		// chronological one — see SRD-091 §3.2. An encoding without that
		// property (epoch seconds as TEXT, or a local time) would make this
		// WHERE clause silently wrong rather than fail.
		list: "SELECT id FROM instances" +
			" WHERE engine_group = ? AND status NOT IN " + claimExcluded +
			" AND (lease_owner = '' OR lease_expiry <= ?)" +
			" ORDER BY id",
		registerGroup: "INSERT INTO groups (group_name) VALUES (?)" +
			" ON CONFLICT DO NOTHING",
		groupExists: "SELECT EXISTS (SELECT 1 FROM groups" +
			" WHERE group_name = ?)",
		ensureTenant: "INSERT INTO tenants" +
			" (tenant_id, engine_group, name) VALUES (?, ?, ?)" +
			" ON CONFLICT DO NOTHING",
		mintDefaultTenant: "INSERT INTO tenants" +
			" (tenant_id, engine_group, name, is_default)" +
			" VALUES (?, ?, 'Default', 1)" +
			" ON CONFLICT DO NOTHING",
		selectDefaultTenant: "SELECT tenant_id FROM tenants" +
			" WHERE engine_group = ? AND is_default = 1",
	}
}
