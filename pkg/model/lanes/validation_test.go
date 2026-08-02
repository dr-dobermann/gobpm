package lanes_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
	"github.com/stretchr/testify/require"
)

// laneOver builds a lane holding the given nodes.
func laneOver(t *testing.T, name string, nodes ...flow.Node) *lanes.Lane {
	t.Helper()

	l, err := lanes.NewLane(name, nil, "", nil)
	require.NoError(t, err)
	require.NoError(t, l.Place(nodes...))

	return l
}

// TestValidateLaneSets — SRD-076 T-5/T-7/T-8: a lane may only place nodes of
// its own container, checked recursively through nested sets.
func TestValidateLaneSets(t *testing.T) {
	mine := node(t, "mine")
	alsoMine := node(t, "also-mine")
	foreign := node(t, "foreign")

	container := []flow.Node{mine, alsoMine}

	t.Run("a well-formed set passes", func(t *testing.T) {
		ls, err := lanes.NewLaneSet("sales",
			[]*lanes.Lane{laneOver(t, "a", mine), laneOver(t, "b", alsoMine)})
		require.NoError(t, err)

		require.NoError(t,
			lanes.ValidateLaneSets([]*lanes.LaneSet{ls}, container))
	})

	t.Run("no lane sets at all passes", func(t *testing.T) {
		require.NoError(t, lanes.ValidateLaneSets(nil, container))
	})

	t.Run("an empty lane set passes", func(t *testing.T) {
		ls, err := lanes.NewLaneSet("empty", nil)
		require.NoError(t, err)

		require.NoError(t,
			lanes.ValidateLaneSets([]*lanes.LaneSet{ls}, container))
	})

	t.Run("a foreign node is refused, naming lane and node",
		func(t *testing.T) {
			ls, err := lanes.NewLaneSet("sales",
				[]*lanes.Lane{laneOver(t, "outsider", foreign)})
			require.NoError(t, err)

			err = lanes.ValidateLaneSets([]*lanes.LaneSet{ls}, container)
			require.Error(t, err)
			require.ErrorContains(t, err, "outsider")
			require.ErrorContains(t, err, "foreign")
			require.ErrorContains(t, err, "isn't in the container")
		})

	t.Run("a nil lane set is refused", func(t *testing.T) {
		require.Error(t,
			lanes.ValidateLaneSets([]*lanes.LaneSet{nil}, container))
	})

	t.Run("every offending lane is reported", func(t *testing.T) {
		other := node(t, "other-foreign")

		ls, err := lanes.NewLaneSet("sales", []*lanes.Lane{
			laneOver(t, "first", foreign),
			laneOver(t, "second", other),
		})
		require.NoError(t, err)

		err = lanes.ValidateLaneSets([]*lanes.LaneSet{ls}, container)
		require.Error(t, err)
		require.ErrorContains(t, err, "first")
		require.ErrorContains(t, err, "second")
	})

	// T-7: the recursion is the point — a nested lane set partitions the SAME
	// container, so a foreign node hiding two levels down is still an error.
	t.Run("a foreign node inside a nested lane set is refused",
		func(t *testing.T) {
			innerSet, err := lanes.NewLaneSet("inner",
				[]*lanes.Lane{laneOver(t, "deep", foreign)})
			require.NoError(t, err)

			outer, err := lanes.NewLane("outer", nil, "", innerSet)
			require.NoError(t, err)
			require.NoError(t, outer.Place(mine))

			outerSet, err := lanes.NewLaneSet("outer-set",
				[]*lanes.Lane{outer})
			require.NoError(t, err)

			err = lanes.ValidateLaneSets(
				[]*lanes.LaneSet{outerSet}, container)
			require.Error(t, err)
			require.ErrorContains(t, err, "deep")
		})

	t.Run("a well-formed nested set passes", func(t *testing.T) {
		innerSet, err := lanes.NewLaneSet("inner",
			[]*lanes.Lane{laneOver(t, "deep", alsoMine)})
		require.NoError(t, err)

		outer, err := lanes.NewLane("outer", nil, "", innerSet)
		require.NoError(t, err)
		require.NoError(t, outer.Place(mine))

		outerSet, err := lanes.NewLaneSet("outer-set", []*lanes.Lane{outer})
		require.NoError(t, err)

		require.NoError(t,
			lanes.ValidateLaneSets([]*lanes.LaneSet{outerSet}, container))
	})
}

// TestWithLaneSetsOption — the option refuses a nil set rather than skipping it:
// dropping a whole partitioning silently is the loss this element prevents.
func TestWithLaneSetsOption(t *testing.T) {
	require.NotNil(t, lanes.WithLaneSets())

	// exercised against the real containers in the process/activities tests;
	// here only the nil guard, which needs no container.
	adder := &fakeAdder{}

	require.Error(t, lanes.WithLaneSets(nil)(adder))
	require.Empty(t, adder.got)

	ls, err := lanes.NewLaneSet("set", nil)
	require.NoError(t, err)

	require.NoError(t, lanes.WithLaneSets(ls)(adder))
	require.Len(t, adder.got, 1)
}

// fakeAdder is a minimal lanes.LaneSetAdder for the option's own guard.
type fakeAdder struct {
	got []*lanes.LaneSet
}

func (a *fakeAdder) Validate() error { return nil }

func (a *fakeAdder) AddLaneSet(ls *lanes.LaneSet) error {
	a.got = append(a.got, ls)

	return nil
}

// failingAdder rejects everything, so WithLaneSets' error-collection path is
// exercised rather than assumed.
type failingAdder struct{}

func (failingAdder) Validate() error { return nil }

func (failingAdder) AddLaneSet(*lanes.LaneSet) error {
	return errs.New(
		errs.M("refused"),
		errs.C("TEST", errs.OperationFailed))
}

// TestWithLaneSetsCollectsAdderErrors covers the branch where the container
// itself rejects a set — every failure is collected, not just the first.
func TestWithLaneSetsCollectsAdderErrors(t *testing.T) {
	first, err := lanes.NewLaneSet("a", nil)
	require.NoError(t, err)

	second, err := lanes.NewLaneSet("b", nil)
	require.NoError(t, err)

	require.Error(t, lanes.WithLaneSets(first, second)(failingAdder{}))
}
