package memrepo_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/repository/repositorytest"
)

// TestConformance proves memrepo against the published Repository
// contract suite (SRD-078 FR-6) — the same suite every durable adapter
// runs.
func TestConformance(t *testing.T) {
	repositorytest.Conformance(t, func(*testing.T) repository.Repository {
		return memrepo.New()
	})
}
