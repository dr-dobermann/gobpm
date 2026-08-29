package data

import (
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
}

// TestAssocConfigValidateNilTarget covers Validate's target==nil guard.
// NewAssociation rejects a nil target before it builds the config, so the
// branch is unreachable through the public constructor — exercised here
// white-box to prove it classifies rather than passing a nil through.
func TestAssocConfigValidateNilTarget(t *testing.T) {
	cfg := asscConfig{src: []*ItemAwareElement{{}}}

	require.ErrorContains(t, cfg.Validate(), "target isn't defined")
}
