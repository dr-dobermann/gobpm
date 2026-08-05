package memrepo_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/renv"
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

// TestClusterDeclaration: memrepo declares itself single-node
// (renv.ClusterAware, SRD-078 FR-3 — satisfied structurally, the store
// itself never imports renv).
func TestClusterDeclaration(t *testing.T) {
	var ca renv.ClusterAware = memrepo.New()

	ok, reason := ca.ClusterCompatibility()
	if ok {
		t.Fatal("an in-memory store can never back a cluster")
	}

	if reason == "" {
		t.Fatal("the declaration must carry its reason")
	}
}
