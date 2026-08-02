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

func (fakeEngine) Type() string { return "##Fake" }

func (fakeEngine) Languages() []string { return []string{"gobpm:goexpr"} }

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

// TestUserTaskResolveEligibility covers the resolution half of the triad — the
// snapshot the engine freezes at distribution (SRD-073 V2). It asserts the
// declared-vs-resolved distinction that keeps a declared-but-empty slot denying
// rather than falling through to the candidate slots.
func TestUserTaskResolveEligibility(t *testing.T) {
	ctx := t.Context()

	t.Run("no triad leaves every slot undeclared and the task open", func(t *testing.T) {
		e := newUT(t).ResolveEligibility(ctx, nil, nil)

		require.False(t, e.Assignee.Declared)
		require.False(t, e.CandidateUsers.Declared)
		require.False(t, e.CandidateGroups.Declared)
		require.True(t, e.Open())
	})

	t.Run("static slots resolve to their identifiers", func(t *testing.T) {
		e := newUT(t,
			activities.WithCandidateUsers("a", "b"),
			activities.WithCandidateGroups("g1"),
		).ResolveEligibility(ctx, nil, nil)

		require.True(t, e.CandidateUsers.Declared)
		require.Equal(t, []string{"a", "b"}, e.CandidateUsers.IDs)
		require.True(t, e.CandidateGroups.Declared)
		require.Equal(t, []string{"g1"}, e.CandidateGroups.IDs)
		require.False(t, e.Assignee.Declared)
		require.False(t, e.Open())
	})

	t.Run("a failed expression stays declared but resolves to nobody", func(t *testing.T) {
		e := newUT(t,
			activities.WithAssigneeExpr(mockdata.NewMockFormalExpression(t)),
		).ResolveEligibility(ctx, nil, fakeEngine{err: errors.New("boom")})

		require.True(t, e.Assignee.Declared,
			"a declared slot must stay declared so it denies")
		require.Empty(t, e.Assignee.IDs)
		require.False(t, e.Open(), "a failed resolution is not an open task")
		require.Error(t, e.Authorize("t1", fakeActor{id: "john"}))
	})
}

// roleWithExpr builds an authorizing role of kind resolving through an
// expression the fakeEngine answers.
func roleWithExpr(
	t *testing.T, kind hi.RoleKind, name string,
) *hi.ResourceRole {
	t.Helper()

	ae, err := hi.NewResourceAssignmentExpression(
		mockdata.NewMockFormalExpression(t))
	require.NoError(t, err)

	build := map[hi.RoleKind]func() (*hi.ResourceRole, error){
		hi.RoleHumanPerformer: func() (*hi.ResourceRole, error) {
			return hi.NewHumanPerformer(name, nil, ae, nil)
		},
		hi.RolePotentialOwner: func() (*hi.ResourceRole, error) {
			return hi.NewPotentialOwner(name, nil, ae, nil)
		},
		hi.RolePerformer: func() (*hi.ResourceRole, error) {
			return hi.NewPerformer(name, nil, ae, nil)
		},
		hi.RoleResource: func() (*hi.ResourceRole, error) {
			return hi.NewResourceRole(name, nil, ae, nil)
		},
	}

	r, err := build[kind]()
	require.NoError(t, err)

	return r
}

// TestUserTaskResolveEligibilityRoles — SRD-075 T-9/T-14: a declared
// authorizing role joins the eligible set through the same resolution path the
// triad uses; a declarative one does not; a task with no role behaves exactly
// as before (ADR-020 v.3 §2.5.4).
func TestUserTaskResolveEligibilityRoles(t *testing.T) {
	ctx := t.Context()
	eng := fakeEngine{val: values.NewVariable([]string{"john", "reviewers"})}

	t.Run("a potential owner populates the role slot", func(t *testing.T) {
		e := newUT(t, activities.WithRoles(
			roleWithExpr(t, hi.RolePotentialOwner, "owners")),
		).ResolveEligibility(ctx, nil, eng)

		require.True(t, e.Roles.Declared)
		require.Equal(t, []string{"john", "reviewers"}, e.Roles.IDs)
		require.False(t, e.Open())
	})

	t.Run("a human performer populates it too", func(t *testing.T) {
		e := newUT(t, activities.WithRoles(
			roleWithExpr(t, hi.RoleHumanPerformer, "approver")),
		).ResolveEligibility(ctx, nil, eng)

		require.True(t, e.Roles.Declared)
		require.Equal(t, []string{"john", "reviewers"}, e.Roles.IDs)
	})

	t.Run("declarative kinds leave the slot undeclared", func(t *testing.T) {
		e := newUT(t, activities.WithRoles(
			roleWithExpr(t, hi.RolePerformer, "machine"),
			roleWithExpr(t, hi.RoleResource, "printer")),
		).ResolveEligibility(ctx, nil, eng)

		require.False(t, e.Roles.Declared)
		require.True(t, e.Open(),
			"declarative roles must not restrict an otherwise-open task")
	})

	t.Run("several authorizing roles union their identifiers",
		func(t *testing.T) {
			e := newUT(t, activities.WithRoles(
				roleWithExpr(t, hi.RolePotentialOwner, "owners"),
				roleWithExpr(t, hi.RoleHumanPerformer, "approver")),
			).ResolveEligibility(ctx, nil, eng)

			require.True(t, e.Roles.Declared)
			require.Len(t, e.Roles.IDs, 4)
		})

	t.Run("a failed role expression stays declared and denies",
		func(t *testing.T) {
			e := newUT(t, activities.WithRoles(
				roleWithExpr(t, hi.RolePotentialOwner, "owners")),
			).ResolveEligibility(ctx, nil, fakeEngine{err: errors.New("boom")})

			require.True(t, e.Roles.Declared)
			require.Empty(t, e.Roles.IDs)
			require.Error(t, e.Authorize("t-1", fakeActor{id: "john"}))
		})

	t.Run("a nil engine leaves the role declared but unresolved",
		func(t *testing.T) {
			e := newUT(t, activities.WithRoles(
				roleWithExpr(t, hi.RolePotentialOwner, "owners")),
			).ResolveEligibility(ctx, nil, nil)

			require.True(t, e.Roles.Declared)
			require.Empty(t, e.Roles.IDs)
		})

	t.Run("no role at all leaves the slot untouched", func(t *testing.T) {
		e := newUT(t).ResolveEligibility(ctx, nil, eng)

		require.False(t, e.Roles.Declared)
		require.Empty(t, e.Roles.IDs)
		require.True(t, e.Open())
	})
}
