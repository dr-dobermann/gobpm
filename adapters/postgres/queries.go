package postgres

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// queries are the adapter's SQL statements, precomputed once at New —
// the schema is the only dynamic fragment, so the per-call texts stay
// constant.
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

// claimExcluded renders the statuses the recovery listing excludes —
// the FR-1 exclusion rule: claimable is non-terminal and not
// suspended, so a growing status vocabulary (e.g. an incidents-holding
// non-terminal status) lists automatically.
var claimExcluded = fmt.Sprintf("(%d, %d, %d)",
	repository.StatusCompleted,
	repository.StatusTerminated,
	repository.StatusSuspended)

// buildQueries renders the statement set for the schema. The only
// interpolated fragments are the schemaRx-validated schema name and
// the constant claimExcluded list; every value travels as a $N
// parameter.
func buildQueries(schema string) queries {
	instances := schema + ".instances"
	tenants := schema + ".tenants"
	groups := schema + ".groups"

	return queries{
		insert: "INSERT INTO " + instances +
			" (id, engine_group, tenant_id, status, payload, rec_version," +
			" lease_owner, lease_incarnation, lease_expiry)" +
			" VALUES ($1, $2, $3, $4, $5, 1, $6, $7, $8)" +
			" ON CONFLICT (id) DO NOTHING",
		update: "UPDATE " + instances +
			" SET engine_group = $2, tenant_id = $3, status = $4," +
			" payload = $5, rec_version = rec_version + 1," +
			" lease_owner = $6, lease_incarnation = $7, lease_expiry = $8," +
			" updated_at = now()" +
			" WHERE id = $1 AND rec_version = $9",
		load: "SELECT engine_group, tenant_id, status, payload," +
			" rec_version, lease_owner, lease_incarnation, lease_expiry" +
			" FROM " + instances + " WHERE id = $1",
		del: "DELETE FROM " + instances + " WHERE id = $1",
		list: "SELECT id FROM " + instances +
			" WHERE engine_group = $1 AND status NOT IN " + claimExcluded +
			" AND (lease_owner = '' OR lease_expiry <= $2)" +
			" ORDER BY id",
		registerGroup: "INSERT INTO " + groups +
			" (group_name) VALUES ($1) ON CONFLICT DO NOTHING",
		groupExists: "SELECT EXISTS (SELECT 1 FROM " + groups +
			" WHERE group_name = $1)",
		ensureTenant: "INSERT INTO " + tenants +
			" (tenant_id, engine_group, name) VALUES ($1, $2, $1)" +
			" ON CONFLICT DO NOTHING",
		mintDefaultTenant: "INSERT INTO " + tenants +
			" (tenant_id, engine_group, name, is_default)" +
			" VALUES ($1, $2, 'Default', true)" +
			" ON CONFLICT DO NOTHING",
		selectDefaultTenant: "SELECT tenant_id FROM " + tenants +
			" WHERE engine_group = $1 AND is_default",
	}
}
