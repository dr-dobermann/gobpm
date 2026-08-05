package thresher_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// SRD-078 T-7 — the FR-3 migration hook: Run migrates a wired
// renv.Migrator repository before the group registry and recovery, and
// a migration failure aborts the start.

// migratingRepo wraps memrepo with a Migrate the tests observe.
type migratingRepo struct {
	*memrepo.Repo
	migrateErr error
	calls      []string
}

func (m *migratingRepo) Migrate(context.Context) error {
	m.calls = append(m.calls, "migrate")

	return m.migrateErr
}

func (m *migratingRepo) RegisterGroup(ctx context.Context, g string) error {
	m.calls = append(m.calls, "register")

	return m.Repo.RegisterGroup(ctx, g)
}

func TestMigratorHook(t *testing.T) {
	t.Run("Run migrates before the group registry", func(t *testing.T) {
		repo := &migratingRepo{Repo: memrepo.New()}

		th, err := thresher.New("mig-1",
			thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
			thresher.WithRepository(repo))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, th.Run(ctx))
		require.Equal(t, []string{"migrate", "register"}, repo.calls,
			"the schema must exist before anything touches it")
	})

	t.Run("a failing migration aborts the start", func(t *testing.T) {
		repo := &migratingRepo{
			Repo:       memrepo.New(),
			migrateErr: context.DeadlineExceeded,
		}

		th, err := thresher.New("mig-2",
			thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
			thresher.WithRepository(repo))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = th.Run(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "migration failed")
		require.Equal(t, []string{"migrate"}, repo.calls,
			"an unmigrated store must never reach the registry")
	})

	t.Run("a non-Migrator repository runs untouched", func(t *testing.T) {
		th, err := thresher.New("mig-3",
			thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
			thresher.WithRepository(memrepo.New()))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, th.Run(ctx))
	})
}
