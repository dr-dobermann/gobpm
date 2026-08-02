package hinteraction_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/stretchr/testify/require"
)

// assignExpr builds a ResourceAssignmentExpression over a mock FormalExpression,
// the shape a role needs to be constructible at all (a nil expression is
// refused).
func assignExpr(t *testing.T) *hinteraction.ResourceAssignmentExpression {
	t.Helper()

	ae, err := hinteraction.NewResourceAssignmentExpression(
		mockdata.NewMockFormalExpression(t))
	require.NoError(t, err)

	return ae
}

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
			_, err := hinteraction.NewResourceRole(
				"role", res, assignExpr(t), nil)
			require.Error(t, err)
		})

	t.Run("parameter bindings without a resource reference are rejected",
		func(t *testing.T) {
			_, err := hinteraction.NewResourceRole("role", nil, assignExpr(t),
				[]hinteraction.ResourceParameterBinding{{}})
			require.Error(t, err)
		})

	t.Run("an assignment expression with no expression is rejected",
		func(t *testing.T) {
			_, err := hinteraction.NewResourceRole("role", nil,
				&hinteraction.ResourceAssignmentExpression{}, nil)
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
		require.Equal(t, hinteraction.RoleResource, rr.Kind())
	})

	t.Run("by assignment expression", func(t *testing.T) {
		rr, err := hinteraction.NewResourceRole("owner", nil, assignExpr(t),
			[]hinteraction.ResourceParameterBinding{})
		require.NoError(t, err)
		require.Equal(t, "owner", rr.Name())
	})

	t.Run("parameter bindings ride along with a resource reference",
		func(t *testing.T) {
			rr, err := hinteraction.NewResourceRole("reviewer", res, nil,
				[]hinteraction.ResourceParameterBinding{{}})
			require.NoError(t, err)
			require.Len(t, rr.ParameterBindings(), 1)
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

// TestRoleConstructorsStampKind checks that every kind-specific constructor
// produces its own kind, so a declared PotentialOwner cannot be mistaken for a
// declarative Performer at resolution time (ADR-020 v.3 §2.5.4).
func TestRoleConstructorsStampKind(t *testing.T) {
	tests := []struct {
		build func() (*hinteraction.ResourceRole, error)
		name  string
		want  hinteraction.RoleKind
	}{
		{
			name: "bare resource role",
			want: hinteraction.RoleResource,
			build: func() (*hinteraction.ResourceRole, error) {
				return hinteraction.NewResourceRole("r", nil, assignExpr(t), nil)
			},
		},
		{
			name: "performer",
			want: hinteraction.RolePerformer,
			build: func() (*hinteraction.ResourceRole, error) {
				return hinteraction.NewPerformer("r", nil, assignExpr(t), nil)
			},
		},
		{
			name: "human performer",
			want: hinteraction.RoleHumanPerformer,
			build: func() (*hinteraction.ResourceRole, error) {
				return hinteraction.NewHumanPerformer(
					"r", nil, assignExpr(t), nil)
			},
		},
		{
			name: "potential owner",
			want: hinteraction.RolePotentialOwner,
			build: func() (*hinteraction.ResourceRole, error) {
				return hinteraction.NewPotentialOwner(
					"r", nil, assignExpr(t), nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, err := tt.build()
			require.NoError(t, err)
			require.Equal(t, tt.want, rr.Kind())
		})
	}
}

// TestRoleMustConstructors covers the panicking twins on both paths.
func TestRoleMustConstructors(t *testing.T) {
	tests := []struct {
		build func(name string) *hinteraction.ResourceRole
		name  string
		want  hinteraction.RoleKind
	}{
		{
			name: "performer",
			want: hinteraction.RolePerformer,
			build: func(n string) *hinteraction.ResourceRole {
				return hinteraction.MustPerformer(n, nil, assignExpr(t), nil)
			},
		},
		{
			name: "human performer",
			want: hinteraction.RoleHumanPerformer,
			build: func(n string) *hinteraction.ResourceRole {
				return hinteraction.MustHumanPerformer(
					n, nil, assignExpr(t), nil)
			},
		},
		{
			name: "potential owner",
			want: hinteraction.RolePotentialOwner,
			build: func(n string) *hinteraction.ResourceRole {
				return hinteraction.MustPotentialOwner(
					n, nil, assignExpr(t), nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" returns the role", func(t *testing.T) {
			rr := tt.build("r")
			require.NotNil(t, rr)
			require.Equal(t, tt.want, rr.Kind())
		})

		t.Run(tt.name+" panics on failure", func(t *testing.T) {
			require.Panics(t, func() { tt.build("") })
		})
	}
}

// TestAuthorizingRoleMustNameSomebody pins FR-3a: an authorizing kind with
// neither a resourceRef nor an assignment expression resolves to the empty set
// by construction, so it is refused where it is written. A declarative kind may
// carry a name alone — there it is a label, not declared authorization that
// authorizes nobody (ADR-020 v.3 §2.5.4).
func TestAuthorizingRoleMustNameSomebody(t *testing.T) {
	tests := []struct {
		build     func() (*hinteraction.ResourceRole, error)
		name      string
		wantError bool
	}{
		{
			name:      "human performer naming nobody is refused",
			wantError: true,
			build: func() (*hinteraction.ResourceRole, error) {
				return hinteraction.NewHumanPerformer("r", nil, nil, nil)
			},
		},
		{
			name:      "potential owner naming nobody is refused",
			wantError: true,
			build: func() (*hinteraction.ResourceRole, error) {
				return hinteraction.NewPotentialOwner("r", nil, nil, nil)
			},
		},
		{
			name: "bare resource role may carry a name alone",
			build: func() (*hinteraction.ResourceRole, error) {
				return hinteraction.NewResourceRole("r", nil, nil, nil)
			},
		},
		{
			name: "performer may carry a name alone",
			build: func() (*hinteraction.ResourceRole, error) {
				return hinteraction.NewPerformer("r", nil, nil, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, err := tt.build()
			if tt.wantError {
				require.Error(t, err)
				require.Nil(t, rr)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, rr)
		})
	}
}

// TestResourceRoleAccessors checks that a role's contents are readable — they
// were write-only before, which is why nothing could resolve one.
func TestResourceRoleAccessors(t *testing.T) {
	res, err := bpmncommon.NewResource("approvers",
		bpmncommon.MustResourceParameter("level", "int", true))
	require.NoError(t, err)

	t.Run("expression mode", func(t *testing.T) {
		ae := assignExpr(t)
		rr, err := hinteraction.NewPotentialOwner("owner", nil, ae, nil)
		require.NoError(t, err)

		require.Same(t, ae, rr.AssignmentExpression())
		require.Nil(t, rr.Resource())
		require.Empty(t, rr.ParameterBindings())
	})

	t.Run("directory mode", func(t *testing.T) {
		rr, err := hinteraction.NewResourceRole("reviewer", res, nil,
			[]hinteraction.ResourceParameterBinding{{}})
		require.NoError(t, err)

		require.Same(t, res, rr.Resource())
		require.Nil(t, rr.AssignmentExpression())
		require.Len(t, rr.ParameterBindings(), 1)
	})

	t.Run("parameter bindings are copied, not aliased", func(t *testing.T) {
		bindings := []hinteraction.ResourceParameterBinding{{}}
		rr, err := hinteraction.NewResourceRole("reviewer", res, nil, bindings)
		require.NoError(t, err)

		got := rr.ParameterBindings()
		require.Len(t, got, 1)
		got[0] = hinteraction.ResourceParameterBinding{
			Parameter: bpmncommon.MustResourceParameter("x", "int", false),
		}

		require.Nil(t, rr.ParameterBindings()[0].Parameter)
	})
}

func TestNewResourceAssignmentExpression(t *testing.T) {
	t.Run("a nil expression is rejected", func(t *testing.T) {
		_, err := hinteraction.NewResourceAssignmentExpression(nil)
		require.Error(t, err)
	})

	t.Run("an invalid base option is propagated", func(t *testing.T) {
		_, err := hinteraction.NewResourceAssignmentExpression(
			mockdata.NewMockFormalExpression(t), foundation.WithID("  "))
		require.Error(t, err)
	})

	t.Run("keeps the expression", func(t *testing.T) {
		expr := mockdata.NewMockFormalExpression(t)
		ae, err := hinteraction.NewResourceAssignmentExpression(expr,
			foundation.WithID("ae-1"))
		require.NoError(t, err)
		require.Same(t, expr, ae.Expression)
		require.Equal(t, "ae-1", ae.ID())
	})
}
