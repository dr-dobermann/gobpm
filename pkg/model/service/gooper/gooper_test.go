package gooper_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// stubReader is a minimal service.DataReader: GetData returns a Ready datum
// per name (from the map), GetDataByID returns the single configured datum.
type stubReader struct {
	byName map[string]data.Data
	byID   data.Data
}

func (s stubReader) GetData(name string) (data.Data, error) {
	if d, ok := s.byName[name]; ok {
		return d, nil
	}

	return nil, errs.New(errs.M("no datum %q", name))
}

func (s stubReader) GetDataByID(string) (data.Data, error) {
	if s.byID == nil {
		return nil, errs.New(errs.M("no datum"))
	}

	return s.byID, nil
}

func (stubReader) GetSources() []string { return nil }

func (stubReader) List(string) ([]string, error) { return nil, nil }

func readyData(t *testing.T, id string, v any) data.Data {
	t.Helper()

	return data.MustParameter(id,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v),
				foundation.WithID(id)),
			data.ReadyDataState))
}

func TestGoOperation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("rejects empty name and nil functor", func(t *testing.T) {
		_, err := gooper.New("",
			func(context.Context, service.DataReader, *data.ItemDefinition) (*data.ItemDefinition, error) {
				return nil, nil
			})
		require.Error(t, err)

		_, err = gooper.New("noop", nil)
		require.Error(t, err)
	})

	t.Run("reader-only functor reads scope and returns its result", func(t *testing.T) {
		op, err := gooper.New("greet",
			func(ctx context.Context, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error) {
				require.Nil(t, in) // no input message declared

				who, err := r.GetData("customer")
				if err != nil {
					return nil, err
				}

				return data.NewItemDefinition(
					values.NewVariable("Hello, " +
						who.Value().Get(ctx).(string)))
			})
		require.NoError(t, err)

		require.Equal(t, gooper.GoOperType, op.Type())
		require.Equal(t, "greet", op.Name())
		require.Contains(t, op.Errors(), errs.OperationFailed)

		out, err := op.Execute(context.Background(),
			stubReader{byName: map[string]data.Data{
				"customer": readyData(t, "customer", "dr.Dobermann"),
			}})
		require.NoError(t, err)
		require.Equal(t, "Hello, dr.Dobermann",
			out.Structure().Get(context.Background()))
	})

	t.Run("functor failure is wrapped", func(t *testing.T) {
		op, err := gooper.New("boom",
			func(context.Context, service.DataReader, *data.ItemDefinition) (*data.ItemDefinition, error) {
				return nil, errs.New(errs.M("kaboom"))
			},
			gooper.WithErrors(errs.TypeCastingError))
		require.NoError(t, err)
		require.Contains(t, op.Errors(), errs.TypeCastingError)

		_, err = op.Execute(context.Background(), stubReader{})
		require.Error(t, err)
	})

	t.Run("composes input + output messages", func(t *testing.T) {
		in := bpmncommon.MustMessage("in",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("n")))
		out := bpmncommon.MustMessage("out",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("r")))

		op, err := gooper.New("double",
			func(ctx context.Context, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error) {
				n := in.Structure().Get(ctx).(int)

				return data.NewItemDefinition(values.NewVariable(n * 2))
			},
			gooper.WithInMessage(in),
			gooper.WithOutMessage(out))
		require.NoError(t, err)

		// scope supplies the input message value; the result fills the output.
		res, err := op.Execute(context.Background(),
			stubReader{byID: readyData(t, "n", 21)})
		require.NoError(t, err)
		require.Equal(t, 42, res.Structure().Get(context.Background()))
	})

	t.Run("clone preserves id and isolates message carriers", func(t *testing.T) {
		in := bpmncommon.MustMessage("in",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("n")))
		out := bpmncommon.MustMessage("out",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("r")))

		op, err := gooper.New("idemp",
			func(ctx context.Context, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error) {
				return data.NewItemDefinition(
					values.NewVariable(in.Structure().Get(ctx).(int) + 1))
			},
			gooper.WithInMessage(in),
			gooper.WithOutMessage(out))
		require.NoError(t, err)

		clone, err := op.Clone()
		require.NoError(t, err)
		require.NotSame(t, op, clone)
		require.Equal(t, op.ID(), clone.ID())

		res, err := clone.Execute(context.Background(),
			stubReader{byID: readyData(t, "n", 9)})
		require.NoError(t, err)
		require.Equal(t, 10, res.Structure().Get(context.Background()))
	})

	t.Run("input binding failure surfaces", func(t *testing.T) {
		in := bpmncommon.MustMessage("in",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("n")))

		op, err := gooper.New("needs-in",
			func(_ context.Context, _ service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error) {
				return in, nil
			},
			gooper.WithInMessage(in))
		require.NoError(t, err)

		// the reader has no datum for the input message → bind fails.
		_, err = op.Execute(context.Background(), stubReader{})
		require.Error(t, err)
	})

	t.Run("output message type mismatch fails", func(t *testing.T) {
		out := bpmncommon.MustMessage("out",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("r"))) // int output

		op, err := gooper.New("wrong-out",
			func(context.Context, service.DataReader, *data.ItemDefinition) (*data.ItemDefinition, error) {
				return data.NewItemDefinition(values.NewVariable("a string"))
			},
			gooper.WithOutMessage(out))
		require.NoError(t, err)

		_, err = op.Execute(context.Background(), stubReader{})
		require.Error(t, err)
	})
}

// TestGoOperationBindInputOnly: a Go operation's BindInputOnly binds its optional
// input message without running the functor. With no input message it returns
// (nil, nil), never touching the reader (SRD-036).
func TestGoOperationBindInputOnly(t *testing.T) {
	op, err := gooper.New("op",
		func(context.Context, service.DataReader, *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	in, err := op.BindInputOnly(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, in)
}
