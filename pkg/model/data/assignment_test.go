package data_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// anExpr is a stand-in FormalExpression for the tests that only need an
// assignment to HAVE one — the package's own way of minting one in tests.
func anExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	return mockdata.NewMockFormalExpression(t)
}

// TestNewAssignment is SRD-097 T-1: both halves are required, and `to` must
// be a data path — the narrowing ADR-011 §2.4 makes.
func TestNewAssignment(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	from := anExpr(t)

	t.Run("both halves are carried", func(t *testing.T) {
		a, err := data.NewAssignment(from, "order.status")
		require.NoError(t, err)
		require.Equal(t, from, a.From())
		require.Equal(t, "order.status", a.To())

		head, steps, err := a.ToHead()
		require.NoError(t, err)
		require.Equal(t, "order", head)
		require.Len(t, steps, 1)
	})

	t.Run("a head-only to is the whole-value write", func(t *testing.T) {
		a, err := data.NewAssignment(from, "order")
		require.NoError(t, err)

		head, steps, err := a.ToHead()
		require.NoError(t, err)
		require.Equal(t, "order", head)
		require.Empty(t, steps)
	})

	t.Run("the to path is trimmed", func(t *testing.T) {
		a, err := data.NewAssignment(from, "  order.status  ")
		require.NoError(t, err)
		require.Equal(t, "order.status", a.To())
	})

	t.Run("a nil from is refused", func(t *testing.T) {
		_, err := data.NewAssignment(nil, "order.status")
		require.Error(t, err)
		require.Contains(t, err.Error(), "NewAssignment")
		require.Contains(t, err.Error(), "from")
	})

	t.Run("a blank to is refused", func(t *testing.T) {
		_, err := data.NewAssignment(from, "   ")
		require.Error(t, err)
		require.Contains(t, err.Error(), "NewAssignment")
		require.Contains(t, err.Error(), "to path")
	})

	t.Run("a to that isn't a path is refused", func(t *testing.T) {
		_, err := data.NewAssignment(from, "order[bad")
		require.Error(t, err)
		require.Contains(t, err.Error(), "isn't a data path")
	})

	t.Run("an invalid base option propagates", func(t *testing.T) {
		_, err := data.NewAssignment(from, "order", foundation.WithID(""))
		require.Error(t, err)
	})
}

// TestAssociationShapeRules is SRD-097 T-2: one shape at a time, and the
// source cardinality that follows from it (§10.4.2 rule 3).
func TestAssociationShapeRules(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	target := func(t *testing.T) *data.ItemAwareElement {
		t.Helper()
		iae, err := data.NewIAE(
			data.WithIDefinition(values.NewVariable(0),
				foundation.WithID("out")),
			data.WithState(data.ReadyDataState))
		require.NoError(t, err)

		return iae
	}

	source := func(t *testing.T, id string) *data.ItemAwareElement {
		t.Helper()

		return data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(1),
				foundation.WithID(id)),
			data.ReadyDataState)
	}

	assign := func(t *testing.T) *data.Assignment {
		t.Helper()
		a, err := data.NewAssignment(anExpr(t), "out")
		require.NoError(t, err)

		return a
	}

	t.Run("a transformation and assignments together are refused",
		func(t *testing.T) {
			_, err := data.NewAssociation(target(t),
				data.WithSource(source(t, "s1")),
				data.WithTransformation(anExpr(t)),
				data.WithAssignments(assign(t)))
			require.Error(t, err)
			require.Contains(t, err.Error(), "one shape at a time")
		})

	t.Run("several sources need a shape", func(t *testing.T) {
		_, err := data.NewAssociation(target(t),
			data.WithSource(source(t, "s1")),
			data.WithSource(source(t, "s2")))
		require.Error(t, err)

		_, err = data.NewAssociation(target(t),
			data.WithSource(source(t, "s1")),
			data.WithSource(source(t, "s2")),
			data.WithTransformation(anExpr(t)))
		require.NoError(t, err)

		_, err = data.NewAssociation(target(t),
			data.WithSource(source(t, "s1")),
			data.WithSource(source(t, "s2")),
			data.WithAssignments(assign(t)))
		require.NoError(t, err)
	})

	t.Run("the assignments read back in order", func(t *testing.T) {
		a1, a2 := assign(t), assign(t)
		a, err := data.NewAssociation(target(t),
			data.WithSource(source(t, "s1")),
			data.WithAssignments(a1, a2))
		require.NoError(t, err)

		got := a.Assignments()
		require.Equal(t, []*data.Assignment{a1, a2}, got)

		got[0] = nil // a copy: the association keeps its own slice
		require.NotNil(t, a.Assignments()[0])
		require.Nil(t, a.Transformation())
	})

	t.Run("an association with no assignments returns none",
		func(t *testing.T) {
			a, err := data.NewAssociation(target(t),
				data.WithSource(source(t, "s1")))
			require.NoError(t, err)
			require.Nil(t, a.Assignments())
		})

	t.Run("a nil assignment is refused with its index", func(t *testing.T) {
		_, err := data.NewAssociation(target(t),
			data.WithSource(source(t, "s1")),
			data.WithAssignments(assign(t), nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "#1")
	})
}
