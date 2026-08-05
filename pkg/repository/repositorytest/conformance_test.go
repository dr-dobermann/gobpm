package repositorytest_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/repository/repositorytest"
)

// TestConformanceSuite proves the suite itself against the reference
// in-memory store — the suite is library code shipped to adapter
// authors, so it carries its own green run (SRD-078 FR-6).
func TestConformanceSuite(t *testing.T) {
	repositorytest.Conformance(t, func(*testing.T) repository.Repository {
		return memrepo.New()
	})
}
