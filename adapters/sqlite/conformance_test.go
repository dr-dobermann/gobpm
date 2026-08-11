package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/sqlite"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/repositorytest"
)

// newFileRepo opens a migrated Repo over a fresh temp-directory database.
func newFileRepo(t *testing.T) *sqlite.Repo {
	t.Helper()

	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "gobpm.db"))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })
	require.NoError(t, repo.Migrate(context.Background()))

	return repo
}

// TestConformanceOnAFile is SRD-091 T-1: the published Repository contract,
// proved against the deployment shape users actually run — a file — and
// **without an environment gate**.
//
// That last part is the point of this adapter. adapters/postgres carries the
// same one-line suite, but every postgres test skips unless GOBPM_PG_TEST_DSN
// is set, which CI never sets — so before this, repositorytest.Conformance
// had only ever executed against memrepo, the implementation it was written
// beside.
func TestConformanceOnAFile(t *testing.T) {
	repositorytest.Conformance(t, func(t *testing.T) repository.Repository {
		return newFileRepo(t)
	})
}

// TestConformanceInMemory is T-2: the same suite through OpenMemory, proving
// the adapter does not depend on a file and that the two constructors agree
// about everything the contract covers.
func TestConformanceInMemory(t *testing.T) {
	repositorytest.Conformance(t, func(t *testing.T) repository.Repository {
		repo, err := sqlite.OpenMemory()
		require.NoError(t, err)

		t.Cleanup(func() { require.NoError(t, repo.Close()) })
		require.NoError(t, repo.Migrate(context.Background()))

		return repo
	})
}
