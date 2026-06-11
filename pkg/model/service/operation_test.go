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

func TestOperation(t *testing.T) {
	// -------------------- Initialization -------------------------------------
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

			require.Error(t, o.Run(context.Background()))
		})

	t.Run("no implementation ouptut",
		func(t *testing.T) {
			o := service.MustOperation("no ouput", nil, out, &exctr{})

			err := o.Run(context.Background())
			t.Log(err.Error())
			require.Error(t, err)
		})

	t.Run("no outgoing message",
		func(t *testing.T) {
			o := service.MustOperation("no outgoing message", in, nil, &exctr{})

			err := o.Run(context.Background())
			t.Log(err.Error())
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			ctx := context.Background()

			o := service.MustOperation("normal", in, out, &exctr{})
			require.Equal(t, 42, o.IncomingMessage().Item().Structure().Get(ctx))
			require.Equal(t, 100, o.OutgoingMessage().Item().Structure().Get(ctx))
			require.Equal(t, "successful executor", o.Type())
			for _, e := range errList {
				require.Contains(t, o.Errors(), e)
			}

			err := o.Run(context.Background())
			require.NoError(t, err)
			require.Equal(t, 42, o.OutgoingMessage().Item().Structure().Get(ctx))
		})

	t.Run("normal fail",
		func(t *testing.T) {
			o := service.MustOperation("normal", in, out,
				&exctr{
					fail: true,
				})

			require.Error(t, o.Run(context.Background()))
		})

	t.Run("normal with empty in and out messages",
		func(t *testing.T) {
			o := service.MustOperation("empty in/out", nil, nil, &exctr{})

			require.NoError(t, o.Run(context.Background()))
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

			// message ids preserved, but carriers are fresh per-instance.
			require.Equal(t,
				o.IncomingMessage().Item().ID(),
				clone.IncomingMessage().Item().ID())
			require.NotSame(t,
				o.IncomingMessage().Item(),
				clone.IncomingMessage().Item())

			// running the clone mutates only the clone's outgoing message.
			require.NoError(t, clone.Run(ctx))
			require.Equal(t, 42, clone.OutgoingMessage().Item().Structure().Get(ctx))
			require.Equal(t, 100, o.OutgoingMessage().Item().Structure().Get(ctx))
		})

	t.Run("clone with empty messages",
		func(t *testing.T) {
			o := service.MustOperation("empty clone", nil, nil, &exctr{})

			clone := o.Clone()
			require.Nil(t, clone.IncomingMessage())
			require.Nil(t, clone.OutgoingMessage())
		})
}
