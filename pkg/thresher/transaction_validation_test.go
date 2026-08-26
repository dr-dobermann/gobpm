package thresher_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// txProcess builds start → each given container → end, every container
// wrapped so the process registers apart from the transaction methods under
// test. A transaction gets a None start and an end inside so its shape
// validates; a plain container the same.
func txProcess(
	t *testing.T, containers ...*activities.SubProcess,
) *process.Process {
	t.Helper()

	proc, err := process.New("tx-coverage")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	require.NoError(t, proc.Add(start))
	require.NoError(t, proc.Add(end))

	var prev flow.Node = start
	for _, c := range containers {
		require.NoError(t, proc.Add(c))
		link(t, prev, c)
		prev = c
	}
	link(t, prev, end)

	return proc
}

// fillContainer gives a container the minimal valid inner shape.
func fillContainer(
	t *testing.T, sp *activities.SubProcess,
) *activities.SubProcess {
	t.Helper()

	s, err := events.NewStartEvent(sp.Name() + "-s")
	require.NoError(t, err)
	e, err := events.NewEndEvent(sp.Name() + "-e")
	require.NoError(t, err)
	require.NoError(t, sp.Add(s))
	require.NoError(t, sp.Add(e))
	link(t, s, e)

	return sp
}

// txSubProcess builds a filled Transaction Sub-Process with the given options.
func txSubProcess(
	t *testing.T, name string, opts ...activities.TransactionOption,
) *activities.SubProcess {
	t.Helper()

	sp, err := activities.NewSubProcess(name, activities.WithTransaction(opts...))
	require.NoError(t, err)

	return fillContainer(t, sp)
}

// TestValidateTransactionCoverage is SRD-093 T-4: registration accepts the
// compensate method and refuses every other, naming each offender.
func TestValidateTransactionCoverage(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	newEngine := func(t *testing.T) *thresher.Thresher {
		t.Helper()
		th, err := thresher.New("tx-coverage-engine")
		require.NoError(t, err)

		return th
	}

	t.Run("compensate registers", func(t *testing.T) {
		_, err := newEngine(t).RegisterProcess(txProcess(t,
			txSubProcess(t, "booking"),
			txSubProcess(t, "explicit",
				activities.WithTransactionMethod(activities.TransactionCompensate))))
		require.NoError(t, err)
	})

	t.Run("a method with no coordinator is refused by name", func(t *testing.T) {
		_, err := newEngine(t).RegisterProcess(txProcess(t,
			txSubProcess(t, "booking",
				activities.WithTransactionMethod("##Store"))))
		require.Error(t, err)
		require.Contains(t, err.Error(), `"booking" (method "##Store")`)
		require.Contains(t, err.Error(), "coordinates compensate only")
		require.Contains(t, err.Error(), "compensation handlers")
	})

	t.Run("several offenders are reported sorted", func(t *testing.T) {
		_, err := newEngine(t).RegisterProcess(txProcess(t,
			txSubProcess(t, "zeta", activities.WithTransactionMethod("image")),
			txSubProcess(t, "alpha", activities.WithTransactionMethod("##Store"))))
		require.Error(t, err)
		msg := err.Error()
		require.Contains(t, msg, "for 2 transaction(s)")
		require.Less(t, strings.Index(msg, `"alpha"`), strings.Index(msg, `"zeta"`))
	})

	t.Run("the walk is deep", func(t *testing.T) {
		outer, err := activities.NewSubProcess("outer")
		require.NoError(t, err)
		inner := txSubProcess(t, "inner",
			activities.WithTransactionMethod("urn:acme:saga"))
		require.NoError(t, outer.Add(inner))
		s, err := events.NewStartEvent("o-s")
		require.NoError(t, err)
		e, err := events.NewEndEvent("o-e")
		require.NoError(t, err)
		require.NoError(t, outer.Add(s))
		require.NoError(t, outer.Add(e))
		link(t, s, inner)
		link(t, inner, e)

		_, err = newEngine(t).RegisterProcess(txProcess(t, outer))
		require.Error(t, err)
		require.Contains(t, err.Error(), `"inner" (method "urn:acme:saga")`)
	})

	t.Run("no transaction, no check", func(t *testing.T) {
		plain, err := activities.NewSubProcess("plain")
		require.NoError(t, err)
		_, err = newEngine(t).RegisterProcess(txProcess(t, fillContainer(t, plain)))
		require.NoError(t, err)
	})
}
