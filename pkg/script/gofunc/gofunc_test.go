package gofunc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/script"
	"github.com/dr-dobermann/gobpm/pkg/script/gofunc"
	"github.com/stretchr/testify/require"
)

// stubReader is a placeholder DataReader: the engine only hands it to the
// script body, so these tests need it non-nil, not functional.
type stubReader struct{}

func (stubReader) GetData(string) (data.Data, error) {
	return nil, errs.New(errs.M("no data"))
}

func (stubReader) GetDataByID(string) (data.Data, error) {
	return nil, errs.New(errs.M("no data"))
}

func (stubReader) GetSources() []string { return nil }

func (stubReader) List(string) ([]string, error) { return nil, nil }

// TestGoFuncIdentity: the engine names its kind and claims its formats, which
// is what the routing registry folds in at thresher.New.
func TestGoFuncIdentity(t *testing.T) {
	e, err := gofunc.New()
	require.NoError(t, err)

	require.Equal(t, gofunc.GoFuncType, e.Type())
	require.Equal(t,
		[]string{"application/x-gobpm-gofunc", "gofunc"}, e.Formats())

	// Formats hands out a copy — a caller mutating it must not silently
	// un-claim the engine's own format.
	got := e.Formats()
	got[0] = "clobbered"
	require.Equal(t, "application/x-gobpm-gofunc", e.Formats()[0])

	var _ script.Engine = e
}

// TestGoFuncBadRegistration: a script that could never be called is refused
// at construction, not at the moment a token reaches the task.
func TestGoFuncBadRegistration(t *testing.T) {
	noop := func(
		context.Context, service.DataReader,
	) (script.Outputs, error) {
		return nil, nil
	}

	for _, tc := range []struct {
		name string
		opt  gofunc.Option
	}{
		{
			name: "an empty name",
			opt:  gofunc.WithScript("   ", noop),
		},
		{
			name: "a nil function",
			opt:  gofunc.WithScript("total", nil),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gofunc.New(tc.opt)
			require.Error(t, err)
		})
	}

	t.Run("a duplicate name", func(t *testing.T) {
		f := noop

		_, err := gofunc.New(
			gofunc.WithScript("total", f),
			gofunc.WithScript("total", f))
		require.Error(t, err)
		require.Contains(t, err.Error(), "already registered")
	})
}

// TestGoFuncExecute: the happy path runs the named function and returns its
// outputs; the error paths each say what was wrong.
func TestGoFuncExecute(t *testing.T) {
	var gotReader service.DataReader

	e, err := gofunc.New(
		gofunc.WithScript("total",
			func(
				_ context.Context, r service.DataReader,
			) (script.Outputs, error) {
				gotReader = r

				return script.Outputs{"sum": values.NewVariable(42)}, nil
			}),
		gofunc.WithScript("boom",
			func(
				context.Context, service.DataReader,
			) (script.Outputs, error) {
				return nil, errors.New("script failed")
			}))
	require.NoError(t, err)

	ctx := context.Background()
	reader := stubReader{}

	t.Run("runs the named script", func(t *testing.T) {
		outs, execErr := e.Execute(ctx, "gofunc", "total", reader)
		require.NoError(t, execErr)
		require.Equal(t, 42, outs["sum"].Get(ctx))
		require.NotNil(t, gotReader, "the body must receive the data reader")
	})

	t.Run("surfaces the script's own error", func(t *testing.T) {
		_, execErr := e.Execute(ctx, "gofunc", "boom", reader)
		require.Error(t, execErr)
		require.Contains(t, execErr.Error(), "script failed")
	})

	t.Run("an unregistered name lists what IS registered", func(t *testing.T) {
		_, execErr := e.Execute(ctx, "gofunc", "nosuch", reader)
		require.Error(t, execErr)
		require.Contains(t, execErr.Error(), "nosuch")
		require.Contains(t, execErr.Error(), "boom, total",
			"the message must name the registered scripts, sorted")
	})

	t.Run("an empty name", func(t *testing.T) {
		_, execErr := e.Execute(ctx, "gofunc", "  ", reader)
		require.Error(t, execErr)
	})

	t.Run("a nil reader", func(t *testing.T) {
		_, execErr := e.Execute(ctx, "gofunc", "total", nil)
		require.Error(t, execErr)
	})
}
