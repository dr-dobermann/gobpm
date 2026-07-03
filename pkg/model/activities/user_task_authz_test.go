package activities_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// fakeActor is a test hi.Actor.
type fakeActor struct {
	id     string
	groups []string
}

func (a fakeActor) UserID() string   { return a.id }
func (a fakeActor) Groups() []string { return a.groups }

// fakeEngine returns a canned value/error for any expression.
type fakeEngine struct {
	val data.Value
	err error
}

func (f fakeEngine) Evaluate(
	_ context.Context,
	_ data.FormalExpression,
	_ data.Source,
) (data.Value, error) {
	return f.val, f.err
}

// newUT builds a UserTask from options or fails the test. A UserTask requires at
// least one output, so a default is always included (the triad is orthogonal).
func newUT(t *testing.T, opts ...options.Option) *activities.UserTask {
	t.Helper()

	all := append(
		[]options.Option{activities.WithOutput("result", "string", true)},
		opts...)

	ut, err := activities.NewUserTask("ut", all...)
	require.NoError(t, err)

	return ut
}

func TestUserTaskAuthorize(t *testing.T) {
	ctx := t.Context()

	t.Run("no triad is open to any actor", func(t *testing.T) {
		require.NoError(t,
			newUT(t).Authorize(ctx, fakeActor{id: "anybody"}, nil, nil))
	})

	t.Run("nil actor rejected", func(t *testing.T) {
		require.Error(t, newUT(t).Authorize(ctx, nil, nil, nil))
	})

	t.Run("assignee is restrictive", func(t *testing.T) {
		ut := newUT(t,
			activities.WithAssignee("john"),
			activities.WithCandidateUsers("mary"))

		require.NoError(t,
			ut.Authorize(ctx, fakeActor{id: "john"}, nil, nil))
		// mary is a candidate but not the assignee → denied.
		require.Error(t,
			ut.Authorize(ctx, fakeActor{id: "mary"}, nil, nil))
	})

	t.Run("candidate user matches by UserID", func(t *testing.T) {
		ut := newUT(t, activities.WithCandidateUsers("a", "b"))
		require.NoError(t, ut.Authorize(ctx, fakeActor{id: "a"}, nil, nil))
		require.Error(t, ut.Authorize(ctx, fakeActor{id: "c"}, nil, nil))
	})

	t.Run("candidate group matches by intersection", func(t *testing.T) {
		ut := newUT(t, activities.WithCandidateGroups("g1", "g2"))
		require.NoError(t,
			ut.Authorize(ctx, fakeActor{id: "x", groups: []string{"g2"}}, nil, nil))
		require.Error(t,
			ut.Authorize(ctx, fakeActor{id: "x", groups: []string{"g9"}}, nil, nil))
	})

	t.Run("expression-resolved candidates", func(t *testing.T) {
		ut := newUT(t, activities.WithCandidateUsersExpr(
			mockdata.NewMockFormalExpression(t)))
		eng := fakeEngine{val: values.NewVariable([]string{"alice"})}

		require.NoError(t,
			ut.Authorize(ctx, fakeActor{id: "alice"}, nil, eng))
		require.Error(t,
			ut.Authorize(ctx, fakeActor{id: "bob"}, nil, eng))
	})

	t.Run("failed expression denies", func(t *testing.T) {
		ut := newUT(t, activities.WithCandidateUsersExpr(
			mockdata.NewMockFormalExpression(t)))
		eng := fakeEngine{err: errors.New("boom")}

		require.Error(t,
			ut.Authorize(ctx, fakeActor{id: "alice"}, nil, eng))
	})

	t.Run("expression-resolved assignee", func(t *testing.T) {
		ut := newUT(t, activities.WithAssigneeExpr(
			mockdata.NewMockFormalExpression(t)))
		eng := fakeEngine{val: values.NewVariable("owner")}

		require.NoError(t,
			ut.Authorize(ctx, fakeActor{id: "owner"}, nil, eng))
		require.Error(t,
			ut.Authorize(ctx, fakeActor{id: "intruder"}, nil, eng))
	})

	t.Run("expression-resolved groups", func(t *testing.T) {
		ut := newUT(t, activities.WithCandidateGroupsExpr(
			mockdata.NewMockFormalExpression(t)))
		eng := fakeEngine{val: values.NewVariable([]string{"admins"})}

		require.NoError(t, ut.Authorize(
			ctx, fakeActor{id: "x", groups: []string{"admins"}}, nil, eng))
		require.Error(t, ut.Authorize(
			ctx, fakeActor{id: "x", groups: []string{"users"}}, nil, eng))
	})
}

func TestUserTaskAssignments(t *testing.T) {
	ut := newUT(t,
		activities.WithAssignee("john"),
		activities.WithCandidateGroups("g1"))

	as := ut.Assignments()
	require.Len(t, as, 2)
	require.Equal(t, hi.Assignee, as[0].Slot())
	require.Equal(t, hi.CandidateGroups, as[1].Slot())

	require.Empty(t, newUT(t).Assignments())
}

func TestUserTaskTriadOptionValidation(t *testing.T) {
	nilExpr := func(o activities.UsrTaskOption) func(*testing.T) {
		return func(t *testing.T) {
			_, err := activities.NewUserTask("ut", o)
			require.Error(t, err)
		}
	}

	// static options reject empty / empty-list identifiers.
	t.Run("empty assignee", nilExpr(activities.WithAssignee("  ")))
	t.Run("empty candidateUsers", nilExpr(activities.WithCandidateUsers()))
	t.Run("empty candidateGroups", nilExpr(activities.WithCandidateGroups()))

	// *Expr options reject a nil expression.
	t.Run("nil assignee expr", nilExpr(activities.WithAssigneeExpr(nil)))
	t.Run("nil candidateUsers expr",
		nilExpr(activities.WithCandidateUsersExpr(nil)))
	t.Run("nil candidateGroups expr",
		nilExpr(activities.WithCandidateGroupsExpr(nil)))

	// each slot takes one Assignment — setting it twice is rejected.
	twice := func(a, b activities.UsrTaskOption) func(*testing.T) {
		return func(t *testing.T) {
			_, err := activities.NewUserTask("ut", a, b)
			require.Error(t, err)
		}
	}

	t.Run("assignee twice",
		twice(activities.WithAssignee("a"), activities.WithAssignee("b")))
	t.Run("candidateUsers twice",
		twice(activities.WithCandidateUsers("a"),
			activities.WithCandidateUsersExpr(mockdata.NewMockFormalExpression(t))))
	t.Run("candidateGroups twice",
		twice(activities.WithCandidateGroups("g1"),
			activities.WithCandidateGroups("g2")))
}

func TestUserTaskValidateOutputs(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	build := func(t *testing.T) *activities.UserTask {
		t.Helper()
		ut, err := activities.NewUserTask("ut",
			activities.WithOutput("name", "string", true),
			activities.WithOutput("age", "int", false))
		require.NoError(t, err)
		return ut
	}

	datum := func(name string, v any) data.Data {
		return data.MustParameter(name,
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(v)),
				data.ReadyDataState))
	}

	t.Run("required present, optional absent", func(t *testing.T) {
		require.NoError(t,
			build(t).ValidateOutputs([]data.Data{datum("name", "John")}))
	})

	t.Run("required and optional present", func(t *testing.T) {
		require.NoError(t, build(t).ValidateOutputs(
			[]data.Data{datum("name", "John"), datum("age", 27)}))
	})

	t.Run("required missing", func(t *testing.T) {
		require.Error(t,
			build(t).ValidateOutputs([]data.Data{datum("age", 27)}))
	})

	t.Run("unknown output rejected", func(t *testing.T) {
		require.Error(t, build(t).ValidateOutputs(
			[]data.Data{datum("name", "John"), datum("x", "y")}))
	})

	t.Run("type mismatch rejected", func(t *testing.T) {
		require.Error(t,
			build(t).ValidateOutputs([]data.Data{datum("name", 42)}))
	})

	t.Run("nil output rejected", func(t *testing.T) {
		require.Error(t, build(t).ValidateOutputs([]data.Data{nil}))
	})
}
