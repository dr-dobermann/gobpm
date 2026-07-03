package hinteraction_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/stretchr/testify/require"
)

// fakeEngine is an expression.Engine that returns a canned value/error for any
// expression, so Assignment.Resolve's expression path can be exercised without
// the real goexpr engine.
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

func TestAssignmentSlotString(t *testing.T) {
	require.Equal(t, "assignee", hi.Assignee.String())
	require.Equal(t, "candidateUsers", hi.CandidateUsers.String())
	require.Equal(t, "candidateGroups", hi.CandidateGroups.String())
	require.Equal(t, "unknown", hi.AssignmentSlot(99).String())
}

func TestNewStaticAssignment(t *testing.T) {
	t.Run("success keeps ids and slot", func(t *testing.T) {
		a, err := hi.NewStaticAssignment(hi.CandidateUsers, "john", "mary")
		require.NoError(t, err)
		require.Equal(t, hi.CandidateUsers, a.Slot())
		require.ElementsMatch(t,
			[]string{"john", "mary"}, a.Resolve(t.Context(), nil, nil))
	})

	t.Run("empty id rejected", func(t *testing.T) {
		_, err := hi.NewStaticAssignment(hi.Assignee, "john", "  ")
		require.Error(t, err)
	})

	t.Run("empty list rejected", func(t *testing.T) {
		_, err := hi.NewStaticAssignment(hi.Assignee)
		require.Error(t, err)
	})
}

func TestNewExprAssignment(t *testing.T) {
	t.Run("success keeps slot", func(t *testing.T) {
		a, err := hi.NewExprAssignment(
			hi.CandidateGroups, mockdata.NewMockFormalExpression(t))
		require.NoError(t, err)
		require.Equal(t, hi.CandidateGroups, a.Slot())
	})

	t.Run("nil expression rejected", func(t *testing.T) {
		_, err := hi.NewExprAssignment(hi.Assignee, nil)
		require.Error(t, err)
	})
}

func TestAssignmentResolve(t *testing.T) {
	expr := mockdata.NewMockFormalExpression(t)

	t.Run("static returns the literal set", func(t *testing.T) {
		a, err := hi.NewStaticAssignment(hi.Assignee, "john")
		require.NoError(t, err)
		require.Equal(t,
			[]string{"john"}, a.Resolve(t.Context(), nil, nil))
	})

	exprAssign := func(t *testing.T) *hi.Assignment {
		t.Helper()
		a, err := hi.NewExprAssignment(hi.CandidateUsers, expr)
		require.NoError(t, err)
		return a
	}

	t.Run("expr with nil engine denies (empty)", func(t *testing.T) {
		require.Empty(t,
			exprAssign(t).Resolve(t.Context(), nil, nil))
	})

	t.Run("expr slice-of-string", func(t *testing.T) {
		eng := fakeEngine{val: values.NewVariable([]string{"a", "b"})}
		require.ElementsMatch(t, []string{"a", "b"},
			exprAssign(t).Resolve(t.Context(), nil, eng))
	})

	t.Run("expr slice-of-any keeps only strings", func(t *testing.T) {
		eng := fakeEngine{val: values.NewVariable([]any{"a", 1, "b", ""})}
		require.ElementsMatch(t, []string{"a", "b"},
			exprAssign(t).Resolve(t.Context(), nil, eng))
	})

	t.Run("expr single string", func(t *testing.T) {
		eng := fakeEngine{val: values.NewVariable("solo")}
		require.Equal(t, []string{"solo"},
			exprAssign(t).Resolve(t.Context(), nil, eng))
	})

	t.Run("expr empty string denies", func(t *testing.T) {
		eng := fakeEngine{val: values.NewVariable("")}
		require.Empty(t, exprAssign(t).Resolve(t.Context(), nil, eng))
	})

	t.Run("expr nil value denies", func(t *testing.T) {
		eng := fakeEngine{val: nil}
		require.Empty(t, exprAssign(t).Resolve(t.Context(), nil, eng))
	})

	t.Run("expr engine error denies", func(t *testing.T) {
		eng := fakeEngine{err: errors.New("boom")}
		require.Empty(t, exprAssign(t).Resolve(t.Context(), nil, eng))
	})

	t.Run("expr unsupported type denies", func(t *testing.T) {
		eng := fakeEngine{val: values.NewVariable(42)}
		require.Empty(t, exprAssign(t).Resolve(t.Context(), nil, eng))
	})
}
