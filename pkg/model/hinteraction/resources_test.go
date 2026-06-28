package hinteraction_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/stretchr/testify/require"
)

func TestNewResourceRole(t *testing.T) {
	res, err := bpmncommon.NewResource("approvers",
		bpmncommon.MustResourceParameter("level", "int", true))
	require.NoError(t, err)

	t.Run("empty name is rejected", func(t *testing.T) {
		_, err := hinteraction.NewResourceRole("  ", res, nil, nil)
		require.Error(t, err)
	})

	t.Run("resource and assignment expression are mutually exclusive",
		func(t *testing.T) {
			ae := &hinteraction.ResourceAssignmentExpression{}
			_, err := hinteraction.NewResourceRole("role", res, ae, nil)
			require.Error(t, err)
		})

	t.Run("an invalid base option is propagated", func(t *testing.T) {
		_, err := hinteraction.NewResourceRole("role", res, nil, nil,
			foundation.WithID("  "))
		require.Error(t, err)
	})

	t.Run("by resource reference", func(t *testing.T) {
		rr, err := hinteraction.NewResourceRole("reviewer", res, nil, nil,
			foundation.WithID("rr-1"))
		require.NoError(t, err)
		require.Equal(t, "reviewer", rr.Name())
		require.Equal(t, "rr-1", rr.ID())
	})

	t.Run("by assignment expression", func(t *testing.T) {
		ae := &hinteraction.ResourceAssignmentExpression{}
		rr, err := hinteraction.NewResourceRole("owner", nil, ae,
			[]hinteraction.ResourceParameterBinding{})
		require.NoError(t, err)
		require.Equal(t, "owner", rr.Name())
	})
}

func TestMustResourceRole(t *testing.T) {
	res, err := bpmncommon.NewResource("approvers",
		bpmncommon.MustResourceParameter("level", "int", true))
	require.NoError(t, err)

	t.Run("returns the role on success", func(t *testing.T) {
		rr := hinteraction.MustResourceRole("reviewer", res, nil, nil)
		require.NotNil(t, rr)
		require.Equal(t, "reviewer", rr.Name())
	})

	t.Run("panics on failure", func(t *testing.T) {
		require.Panics(t, func() {
			hinteraction.MustResourceRole("", res, nil, nil)
		})
	})
}
