package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// ioAdder records what the options deliver, the way a container config
// would.
type ioAdder struct {
	got map[data.Direction][]*data.Parameter
}

func (a *ioAdder) AddIOParameters(
	dir data.Direction, params ...*data.Parameter,
) error {
	if a.got == nil {
		a.got = map[data.Direction][]*data.Parameter{}
	}

	a.got[dir] = append(a.got[dir], params...)

	return nil
}

func intParam(t *testing.T, name string) *data.Parameter {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0)),
			data.ReadyDataState))
}

// TestWithInputsOutputs — SRD-093 T-1: each option lands in its direction,
// accumulates across calls, and refuses a nil parameter naming its index.
func TestWithInputsOutputs(t *testing.T) {
	a, b, c := intParam(t, "a"), intParam(t, "b"), intParam(t, "c")

	t.Run("each option lands in its direction and accumulates",
		func(t *testing.T) {
			adder := &ioAdder{}

			require.NoError(t, data.WithInputs(a)(adder))
			require.NoError(t, data.WithInputs(b)(adder))
			require.NoError(t, data.WithOutputs(c)(adder))

			require.Len(t, adder.got[data.Input], 2)
			require.Equal(t, "a", adder.got[data.Input][0].Name())
			require.Equal(t, "b", adder.got[data.Input][1].Name())
			require.Len(t, adder.got[data.Output], 1)
			require.Equal(t, "c", adder.got[data.Output][0].Name())
		})

	t.Run("the option carries the options.Option marker",
		func(t *testing.T) {
			// the marker is what lets process.New take the option among
			// its other options; it does nothing else
			var opt options.Option = data.WithInputs(a)
			opt.Option()
		})

	t.Run("a nil parameter is refused naming the option and the index",
		func(t *testing.T) {
			adder := &ioAdder{}

			err := data.WithInputs(a, nil)(adder)
			require.Error(t, err)
			require.Contains(t, err.Error(), "WithInputs")
			require.Contains(t, err.Error(), "nil parameter")
			require.Empty(t, adder.got, "nothing lands when the batch is refused")

			err = data.WithOutputs(nil)(adder)
			require.ErrorContains(t, err, "WithOutputs")
		})

	t.Run("it is an options.Option", func(t *testing.T) {
		var _ options.Option = data.WithInputs(a)
		var _ options.Option = data.WithOutputs(c)
	})
}
