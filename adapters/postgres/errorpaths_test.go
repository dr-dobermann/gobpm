package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/postgres"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// closedRepo builds a Repo over a CLOSED pool — every database touch
// fails, which drives the loud-error paths without a server (NFR-2).
func closedRepo(t *testing.T) *postgres.Repo {
	t.Helper()

	db, err := sql.Open("pgx",
		"postgres://nobody:nothing@localhost:1/void?sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	repo, err := postgres.New(db)
	require.NoError(t, err)

	return repo
}

func TestDatabaseFailuresAreLoud(t *testing.T) {
	repo := closedRepo(t)
	ctx := context.Background()

	rec := repository.InstanceRecord{
		ID: "i-1", Group: "g", Status: repository.StatusActive,
	}

	for name, call := range map[string]func() error{
		"Save": func() error { return repo.Save(ctx, rec) },
		"Load": func() error {
			_, _, err := repo.Load(ctx, "i-1")

			return err
		},
		"Delete": func() error { return repo.Delete(ctx, "i-1") },
		"ListInFlight": func() error {
			_, err := repo.ListInFlight(ctx, "g", time.Now())

			return err
		},
		"RegisterGroup": func() error {
			return repo.RegisterGroup(ctx, "g")
		},
		"GroupExists": func() error {
			_, err := repo.GroupExists(ctx, "g")

			return err
		},
		"Migrate": func() error { return repo.Migrate(ctx) },
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, call(),
				"%s over a dead pool must fail loud", name)
		})
	}
}
