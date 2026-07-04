package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAssociationNilTargetGuards covers the defensive target==nil guards in the
// Association accessors. NewAssociation rejects a nil target, so these are
// unreachable through the public constructor — exercised here white-box via a
// zero-value Association to prove the guards behave (no nil-dereference).
func TestAssociationNilTargetGuards(t *testing.T) {
	a := &Association{} // target is nil

	require.False(t, a.IsReady())
	require.Equal(t, "", a.TargetItemDefID())

	_, err := a.Value(context.Background())
	require.Error(t, err)
}

// TestAssociationCalculateNoSources covers calculate's no-sources guard: no
// transformation and an empty source set. Unreachable through NewAssociation
// (which requires a source or a transformation), it guards a would-be
// index-out-of-range on SourcesIDs()[0] and returns a classified error instead.
func TestAssociationCalculateNoSources(t *testing.T) {
	a := &Association{} // nil transformation, empty sources

	require.Error(t, a.calculate(context.Background()))
}
