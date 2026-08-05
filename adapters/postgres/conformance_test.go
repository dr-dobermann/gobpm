package postgres_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/repositorytest"
)

// TestConformance proves the adapter against the published Repository
// contract suite (SRD-078 T-2) — the same suite memrepo passes: one
// truth, two backends. Every factory call gets its own schema.
func TestConformance(t *testing.T) {
	repositorytest.Conformance(t, func(t *testing.T) repository.Repository {
		return newRepo(t)
	})
}
