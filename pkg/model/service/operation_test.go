package service_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
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

func (e *exctr) Execute(in *data.ItemDefinition) (*data.ItemDefinition, error) {
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
	in := common.MustMessage("test_input_msg",
		data.MustItemDefinition(values.NewVariable(42)))
	out := common.MustMessage("test_out_msg",
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

			require.Error(t, o.Run())
		})

	t.Run("no implementation ouptut",
		func(t *testing.T) {
			o := service.MustOperation("no ouput", nil, out, &exctr{})

			err := o.Run()
			t.Log(err.Error())
			require.Error(t, err)
		})

	t.Run("no outgoing message",
		func(t *testing.T) {
			o := service.MustOperation("no outgoing message", in, nil, &exctr{})

			err := o.Run()
			t.Log(err.Error())
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			o := service.MustOperation("normal", in, out, &exctr{})
			require.Equal(t, 42, o.IncomingMessage().Item().Structure().Get())
			require.Equal(t, 100, o.OutgoingMessage().Item().Structure().Get())
			require.Equal(t, "successful executor", o.Type())
			for _, e := range errList {
				require.Contains(t, o.Errors(), e)
			}

			err := o.Run()
			require.NoError(t, err)
			require.Equal(t, 42, o.OutgoingMessage().Item().Structure().Get())
		})

	t.Run("normal fail",
		func(t *testing.T) {
			o := service.MustOperation("normal", in, out,
				&exctr{
					fail: true,
				})

			require.Error(t, o.Run())
		})

	t.Run("normal with empty in and out messages",
		func(t *testing.T) {
			o := service.MustOperation("empty in/out", nil, nil, &exctr{})

			require.NoError(t, o.Run())
		})
}
