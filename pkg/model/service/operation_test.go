package service_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

// test implementators building
type exctr struct {
	fail bool
}

func (e *exctr) Type() string {
	if e.fail {
		return "failing executor"
	}

	return "successful executor"
}

func (e *exctr) ErrorClasses() []string {
	return []string{errs.OperationFailed}
}

func (e *exctr) Execute(
	_ context.Context,
	in *data.ItemDefinition,
) (*data.ItemDefinition, error) {
	if e.fail {
		return nil, errs.New(
			errs.M("operation failed by default"),
			errs.C(errs.OperationFailed))
	}

	return in, nil
}

// wrongOut is an Implementor that returns a string result regardless of input,
// used to drive the output type-mismatch path against an int output message.
type wrongOut struct{}

func (wrongOut) Type() string { return "wrong-out" }

func (wrongOut) ErrorClasses() []string { return nil }

func (wrongOut) Execute(
	_ context.Context,
	_ *data.ItemDefinition,
) (*data.ItemDefinition, error) {
	return data.MustItemDefinition(values.NewVariable("string-result")), nil
}

// stubReader is a minimal service.DataReader returning a single Ready datum
// for any lookup (or an error when empty), used to drive messageOperation
// input binding in isolation.
type stubReader struct {
	d data.Data
}

func (s stubReader) GetData(string) (data.Data, error) {
	if s.d == nil {
		return nil, errs.New(errs.M("no data"))
	}

	return s.d, nil
}

func (s stubReader) GetDataByID(string) (data.Data, error) {
	if s.d == nil {
		return nil, errs.New(errs.M("no data"))
	}

	return s.d, nil
}

func (stubReader) GetSources() []string { return nil }

func (stubReader) List(string) ([]string, error) { return nil, nil }

// readyData builds a Ready datum carrying v, the shape an input binding reads.
func readyData(v any) data.Data {
	return data.MustParameter("in",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v)),
			data.ReadyDataState))
}

func TestOperation(t *testing.T) {
	// -------------------- Initialization -------------------------------------
	require.NoError(t, data.CreateDefaultStates())

	// test messages building
	in := bpmncommon.MustMessage("test_input_msg",
		data.MustItemDefinition(values.NewVariable(42)))
	out := bpmncommon.MustMessage("test_out_msg",
		data.MustItemDefinition(values.NewVariable(100)))

	errList := []string{
		errs.ObjectNotFound,
		errs.OperationFailed,
		errs.EmptyNotAllowed,
	}

	// --------------------- Testing -------------------------------------------
	t.Run("empty op name",
		func(t *testing.T) {
			_, err := service.NewOperation("", nil, nil, nil)
			require.Error(t, err)
		})

	t.Run("invalid option",
		func(t *testing.T) {
			require.Panics(t,
				func() {
					_ = service.MustOperation("panic", nil, nil, nil,
						options.WithName("invalid name"))
				})
		})

	t.Run("empty implementor call",
		func(t *testing.T) {
			o := service.MustOperation("empty implementor call", nil, nil, nil)
			require.Equal(t, "empty implementor call", o.Name())
			require.Equal(t, service.UnspecifiedImplementation, o.Type())

			_, err := o.Execute(context.Background(), nil)
			require.Error(t, err)
		})

	t.Run("no implementation ouptut",
		func(t *testing.T) {
			o := service.MustOperation("no ouput", nil, out, &exctr{})

			_, err := o.Execute(context.Background(), nil)
			t.Log(err.Error())
			require.Error(t, err)
		})

	t.Run("no outgoing message",
		func(t *testing.T) {
			o := service.MustOperation("no outgoing message", in, nil, &exctr{})

			_, err := o.Execute(context.Background(), stubReader{readyData(42)})
			t.Log(err.Error())
			require.Error(t, err)
		})

	t.Run("input not found in scope",
		func(t *testing.T) {
			o := service.MustOperation("missing input", in, out, &exctr{})

			_, err := o.Execute(context.Background(), stubReader{})
			require.Error(t, err)
		})

	t.Run("input not ready",
		func(t *testing.T) {
			o := service.MustOperation("unready input", in, out, &exctr{})

			notReady := data.MustParameter("in",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(42)),
					data.UnavailableDataState))

			_, err := o.Execute(context.Background(), stubReader{notReady})
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			ctx := context.Background()

			o := service.MustOperation("normal", in, out, &exctr{})
			require.Equal(t, "successful executor", o.Type())
			for _, e := range errList {
				require.Contains(t, o.Errors(), e)
			}

			// the implementation echoes the bound input (42) into the output.
			result, err := o.Execute(ctx, stubReader{readyData(42)})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 42, result.Structure().Get(ctx))
		})

	t.Run("normal fail",
		func(t *testing.T) {
			o := service.MustOperation("normal", in, out,
				&exctr{
					fail: true,
				})

			_, err := o.Execute(context.Background(), stubReader{readyData(42)})
			require.Error(t, err)
		})

	t.Run("input type mismatch fails the bind",
		func(t *testing.T) {
			o := service.MustOperation("bind mismatch", in, out, &exctr{})

			// scope holds a string where the int input message expects an int.
			_, err := o.Execute(context.Background(),
				stubReader{readyData("not-an-int")})
			require.Error(t, err)
		})

	t.Run("output type mismatch fails the produce",
		func(t *testing.T) {
			o := service.MustOperation("out mismatch", nil, out, wrongOut{})

			_, err := o.Execute(context.Background(), nil)
			require.Error(t, err)
		})

	t.Run("normal with empty in and out messages",
		func(t *testing.T) {
			o := service.MustOperation("empty in/out", nil, nil, &exctr{})

			result, err := o.Execute(context.Background(), nil)
			require.NoError(t, err)
			require.Nil(t, result)
		})

	t.Run("clone shares definition, isolates message state",
		func(t *testing.T) {
			ctx := context.Background()

			localIn := bpmncommon.MustMessage("clone_in_msg",
				data.MustItemDefinition(values.NewVariable(42)))
			localOut := bpmncommon.MustMessage("clone_out_msg",
				data.MustItemDefinition(values.NewVariable(100)))

			o := service.MustOperation("clone me", localIn, localOut, &exctr{})

			clone := o.Clone()

			// identity preserved, independent object.
			require.NotSame(t, o, clone)
			require.Equal(t, o.ID(), clone.ID())
			require.Equal(t, o.Name(), clone.Name())
			require.Equal(t, o.Type(), clone.Type())

			// running the clone produces its result without touching the
			// original — its message carriers are fresh per-instance.
			result, err := clone.Execute(ctx, stubReader{readyData(7)})
			require.NoError(t, err)
			require.Equal(t, 7, result.Structure().Get(ctx))

			// the original, run independently, yields its own input value.
			oResult, err := o.Execute(ctx, stubReader{readyData(42)})
			require.NoError(t, err)
			require.Equal(t, 42, oResult.Structure().Get(ctx))
		})

	t.Run("clone with empty messages",
		func(t *testing.T) {
			o := service.MustOperation("empty clone", nil, nil, &exctr{})

			clone := o.Clone()
			require.Equal(t, o.ID(), clone.ID())

			result, err := clone.Execute(context.Background(), nil)
			require.NoError(t, err)
			require.Nil(t, result)
		})
}

// TestMessageOperationBindInputOnly: BindInputOnly binds the operation's input
// message without running the implementation. With no input message it binds
// nothing and returns (nil, nil), never touching the reader (SRD-036).
func TestMessageOperationBindInputOnly(t *testing.T) {
	op := service.MustOperation("op", nil, nil, nil)

	in, err := op.BindInputOnly(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, in)
}
