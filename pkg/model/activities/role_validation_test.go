package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

// SRD-075 T-6/T-8/T-8a — ValidateResourceRoles: an authorizing role naming its
// people through a resourceRef needs a directory the engine doesn't provide, so
// it is refused at registration; a declarative role in the same shape is fine.

// directoryResource is the Resource an unsatisfiable role points at.
func directoryResource(t *testing.T) *bpmncommon.Resource {
	t.Helper()

	res, err := bpmncommon.NewResource("approvers",
		bpmncommon.MustResourceParameter("level", "int", true))
	require.NoError(t, err)

	return res
}

// roleExpr builds the assignment expression an executable role carries.
func roleExpr(t *testing.T) *hi.ResourceAssignmentExpression {
	t.Helper()

	ae, err := hi.NewResourceAssignmentExpression(
		mockdata.NewMockFormalExpression(t))
	require.NoError(t, err)

	return ae
}

// taskWithRoles builds a ServiceTask carrying roles.
func taskWithRoles(
	t *testing.T, name string, roles ...*hi.ResourceRole,
) *activities.ServiceTask {
	t.Helper()

	st, err := activities.NewServiceTask(name,
		service.MustOperation(name+"-op", nil, nil, nil),
		[]options.Option{
			activities.WithoutParams(),
			activities.WithRoles(roles...),
		}...)
	require.NoError(t, err)

	return st
}

func TestValidateResourceRoles(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	res := directoryResource(t)

	t.Run("directory-mode potential owner on a node is refused",
		func(t *testing.T) {
			owner, err := hi.NewPotentialOwner("owners", res, nil, nil)
			require.NoError(t, err)

			err = activities.ValidateResourceRoles(
				[]flow.Node{taskWithRoles(t, "work", owner)}, nil)

			require.Error(t, err)
			require.ErrorContains(t, err, "owners")
			require.ErrorContains(t, err, "work")
			require.ErrorContains(t, err, "organizational directory")
		})

	t.Run("directory-mode human performer on a node is refused",
		func(t *testing.T) {
			perf, err := hi.NewHumanPerformer("approver", res, nil, nil)
			require.NoError(t, err)

			err = activities.ValidateResourceRoles(
				[]flow.Node{taskWithRoles(t, "work", perf)}, nil)
			require.Error(t, err)
		})

	t.Run("a container's own roles are checked", func(t *testing.T) {
		owner, err := hi.NewPotentialOwner("owners", res, nil, nil)
		require.NoError(t, err)

		err = activities.ValidateResourceRoles(nil,
			[]*hi.ResourceRole{owner})
		require.Error(t, err)
	})

	t.Run("expression-mode authorizing role is accepted",
		func(t *testing.T) {
			owner, err := hi.NewPotentialOwner(
				"owners", nil, roleExpr(t), nil)
			require.NoError(t, err)

			require.NoError(t, activities.ValidateResourceRoles(
				[]flow.Node{taskWithRoles(t, "work", owner)}, nil))
		})

	t.Run("declarative roles in directory mode are accepted",
		func(t *testing.T) {
			bare, err := hi.NewResourceRole("printer", res, nil, nil)
			require.NoError(t, err)

			perf, err := hi.NewPerformer("scanner", res, nil, nil)
			require.NoError(t, err)

			require.NoError(t, activities.ValidateResourceRoles(
				[]flow.Node{taskWithRoles(t, "work", bare, perf)}, nil))
		})

	t.Run("a node carrying no roles is skipped", func(t *testing.T) {
		require.NoError(t, activities.ValidateResourceRoles(
			[]flow.Node{taskWithRoles(t, "work")}, nil))
	})

	t.Run("every offending role is reported", func(t *testing.T) {
		first, err := hi.NewPotentialOwner("first", res, nil, nil)
		require.NoError(t, err)

		second, err := hi.NewHumanPerformer("second", res, nil, nil)
		require.NoError(t, err)

		err = activities.ValidateResourceRoles(
			[]flow.Node{taskWithRoles(t, "work", first, second)}, nil)

		require.Error(t, err)
		require.ErrorContains(t, err, "first")
		require.ErrorContains(t, err, "second")
	})
}

// TestValidateResourceRolesNested checks that the check reaches a role declared
// inside a nested Sub-Process: SubProcess.Validate runs it over its own nodes
// and recurses into its children's Validate hooks.
func TestValidateResourceRolesNested(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	owner, err := hi.NewPotentialOwner(
		"owners", directoryResource(t), nil, nil)
	require.NoError(t, err)

	inner, err := activities.NewSubProcess("inner")
	require.NoError(t, err)
	require.NoError(t, inner.Add(taskWithRoles(t, "deep", owner)))

	err = activities.ValidateResourceRoles([]flow.Node{inner}, nil)
	require.NoError(t, err,
		"the outer pass sees the Sub-Process's own roles, not its children's")

	// the child's role surfaces through the Sub-Process's own Validate hook.
	require.ErrorContains(t, inner.Validate(), "owners")
}

// TestSubProcessLaneSets — SRD-076 T-9: a Sub-Process is the other
// FlowElementsContainer BPMN hangs laneSets off, so it accepts, exposes and
// validates them exactly as a Process does.
func TestSubProcessLaneSets(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inner := taskWithRoles(t, "inner")

	outsideNode := taskWithRoles(t, "outside")

	t.Run("carried and exposed", func(t *testing.T) {
		lane, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)
		require.NoError(t, lane.Place(inner))

		ls, err := lanes.NewLaneSet("org", []*lanes.Lane{lane})
		require.NoError(t, err)

		sp, err := activities.NewSubProcess("body", lanes.WithLaneSets(ls))
		require.NoError(t, err)
		require.NoError(t, sp.Add(inner))

		require.Len(t, sp.LaneSets(), 1)
		require.Equal(t, "org", sp.LaneSets()[0].Name())
	})

	t.Run("a lane placing a foreign node fails validation",
		func(t *testing.T) {
			lane, err := lanes.NewLane("outsider", nil, "", nil)
			require.NoError(t, err)
			require.NoError(t, lane.Place(outsideNode))

			ls, err := lanes.NewLaneSet("org", []*lanes.Lane{lane})
			require.NoError(t, err)

			sp, err := activities.NewSubProcess("body", lanes.WithLaneSets(ls))
			require.NoError(t, err)
			require.NoError(t, sp.Add(taskWithRoles(t, "own")))

			require.ErrorContains(t, sp.Validate(), "outsider")
		})
}

// TestSubProcessArtifacts — SRD-092 T-5: a Sub-Process is the other container
// BPMN hangs artifacts off, so it accepts and exposes them exactly as a
// Process does.
func TestSubProcessArtifacts(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)

	note := artifacts.MustTextAnnotation("inner note", "",
		foundation.WithID("sp-note"))
	grp := artifacts.MustGroup("inner", foundation.WithID("sp-grp"))

	t.Run("accumulates in order and returns a copy", func(t *testing.T) {
		require.NoError(t, sp.AddArtifacts(note))
		require.NoError(t, sp.AddArtifacts(
			artifacts.MustAssociation(note, grp, artifacts.One,
				foundation.WithID("sp-a"))))

		arts := sp.Artifacts()
		require.Len(t, arts, 2)
		require.Equal(t, "sp-note", arts[0].ID())
		require.Equal(t, "sp-a", arts[1].ID())

		arts[0] = nil
		require.Equal(t, "sp-note", sp.Artifacts()[0].ID())
	})

	t.Run("a nil artifact is refused, the collection unchanged",
		func(t *testing.T) {
			require.ErrorContains(t, sp.AddArtifacts(nil), "nil artifact")
			require.Len(t, sp.Artifacts(), 2)
		})

	t.Run("a duplicate id is refused", func(t *testing.T) {
		err := sp.AddArtifacts(artifacts.MustTextAnnotation("x", "",
			foundation.WithID("sp-note")))
		require.ErrorContains(t, err, "duplicate artifact id")
	})
}
