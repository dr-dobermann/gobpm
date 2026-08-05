package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// SRD-078 T-4 — migrations: fresh-db create, idempotent re-run, the
// recorded version, the database-enforced single default tenant, and
// the group-local idempotent default-tenant ensure.

func TestMigrate(t *testing.T) {
	ctx := context.Background()

	t.Run("a fresh schema serves the contract and records its version",
		func(t *testing.T) {
			repo := newRepo(t) // newRepo migrates

			require.NoError(t, repo.RegisterGroup(ctx, "g"))
			require.NoError(t, repo.Save(ctx, repository.InstanceRecord{
				ID: "i-1", Group: "g", Status: repository.StatusActive,
				Payload: []byte(`{"schema":1}`),
			}))

			ids, err := repo.ListInFlight(ctx, "g", time.Now())
			require.NoError(t, err)
			require.Equal(t, []string{"i-1"}, ids)
		})

	t.Run("re-running over an up-to-date schema is a no-op",
		func(t *testing.T) {
			repo := newRepo(t)

			require.NoError(t, repo.RegisterGroup(ctx, "g"))
			require.NoError(t, repo.Save(ctx, repository.InstanceRecord{
				ID: "keep", Group: "g", Status: repository.StatusActive,
			}))

			require.NoError(t, repo.Migrate(ctx),
				"the second Migrate must be a clean no-op")

			_, ok, err := repo.Load(ctx, "keep")
			require.NoError(t, err)
			require.True(t, ok, "a re-run must never touch existing data")

			var version, rows int
			require.NoError(t, openDB(t).QueryRowContext(ctx,
				"SELECT COALESCE(MAX(version), 0), count(*) FROM "+
					repo.Schema()+".schema_version").
				Scan(&version, &rows))
			require.Equal(t, 1, version, "migration 0001 must be recorded")
			require.Equal(t, 1, rows, "a re-run must record nothing new")
		})

	t.Run("the database rejects a second default tenant per group",
		func(t *testing.T) {
			repo := newRepo(t)
			db := openDB(t)

			require.NoError(t, repo.RegisterGroup(ctx, "g"))

			// the first default arrives via the store-side resolution.
			require.NoError(t, repo.Save(ctx, repository.InstanceRecord{
				ID: "i-1", Group: "g", Status: repository.StatusActive,
			}))

			_, err := db.ExecContext(ctx,
				"INSERT INTO "+repo.Schema()+".tenants"+
					" (tenant_id, engine_group, name, is_default)"+
					" VALUES ('rogue', 'g', 'Rogue', true)")
			require.Error(t, err,
				"the partial unique index must reject a second default")
			require.Contains(t, err.Error(), "tenants_one_default_per_group")
		})

	t.Run("a taken default id with no default flag fails loud",
		func(t *testing.T) {
			repo := newRepo(t)
			db := openDB(t)

			require.NoError(t, repo.RegisterGroup(ctx, "g"))

			// the 'default' NAME is occupied by a NON-default tenant, so
			// the mint conflicts and no flag-designated row exists — the
			// resolution must refuse rather than guess (the flag is the
			// designation, never the id).
			_, err := db.ExecContext(ctx,
				"INSERT INTO "+repo.Schema()+".tenants"+
					" (tenant_id, engine_group, name) VALUES"+
					" ('default', 'g', 'Occupied')")
			require.NoError(t, err)

			err = repo.Save(ctx, repository.InstanceRecord{
				ID: "i-1", Group: "g", Status: repository.StatusActive,
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "no default tenant")
		})

	t.Run("the default-tenant ensure is idempotent and group-local",
		func(t *testing.T) {
			repo := newRepo(t)
			db := openDB(t)

			for _, g := range []string{"g-1", "g-2"} {
				require.NoError(t, repo.RegisterGroup(ctx, g))

				// two ""-tenant saves per group: one mint, one reuse.
				for _, id := range []string{g + "-a", g + "-b"} {
					require.NoError(t, repo.Save(ctx,
						repository.InstanceRecord{
							ID: id, Group: g,
							Status: repository.StatusActive,
						}))
				}
			}

			var defaults int
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT count(*) FROM "+repo.Schema()+".tenants"+
					" WHERE is_default").Scan(&defaults))
			require.Equal(t, 2, defaults,
				"each group holds exactly ONE default tenant row")
		})
}
