package service_test

import (
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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

func (e *exctr) Execute(op *service.Operation) (any, error) {
	if e.fail {
		return nil,
			&errs.ApplicationError{
				Message: "Operation failed by default",
				Classes: []string{
					errs.OperationFailed},
			}
	}

	return "operation " + op.Name() + " executed sucessfully", nil
}

func TestNewOperation(t *testing.T) {
	type args struct {
		name          string
		inMsg, outMsg *common.Message
		errList       []string
		executor      service.Executor
		baseOpts      []options.Option
	}

	type expectations struct {
		name, id      string
		inMsg, outMsg *common.Message
		errList       []string
		executor      service.Executor
	}

	// -------------------- Initialization -------------------------------------
	// test messages building
	in := common.MustMessage("test_input_msg", nil)
	out := common.MustMessage("test_out_msg", nil)

	errList := []string{errs.OperationFailed}

	tstExctr := &exctr{}

	// --------------------- Testing -------------------------------------------
	// test cases
	tests := []struct {
		name string
		args args
		want expectations
	}{
		{
			name: "empty operation name",
			args: args{
				name:     "",
				errList:  []string{},
				baseOpts: []options.Option{}},
			want: expectations{
				name:    "empty_operation",
				errList: []string{}},
		},
		{
			name: "empty operation",
			args: args{
				name:     "empty_operation",
				errList:  []string{},
				baseOpts: []options.Option{}},
			want: expectations{
				name:    "empty_operation",
				errList: []string{}},
		},
		{
			name: "full_operation",
			args: args{
				name:     "empty_operation",
				inMsg:    in,
				outMsg:   out,
				errList:  errList,
				executor: tstExctr,
				baseOpts: []options.Option{foundation.WithId("test_op")}},
			want: expectations{
				name:     "empty_operation",
				id:       "test_op",
				inMsg:    in,
				outMsg:   out,
				errList:  errList,
				executor: tstExctr},
		},
	}

	for _, tst := range tests {
		t.Run(tst.name,
			func(t *testing.T) {
				o, err := service.NewOperation(
					tst.args.name,
					tst.args.inMsg,
					tst.args.outMsg,
					tst.args.executor,
					tst.args.baseOpts...,
				)

				// check empty name
				if tst.args.name == "" {
					require.Error(t, err)
					require.Empty(t, o)

					return
				}

				// check name
				require.NoError(t, err)
				require.NotEmpty(t, o)
				require.Equal(t, tst.want.name, o.Name())

				// check incoming message
				if tst.want.inMsg != nil {
					in := o.IncomingMessage()
					require.NotEmpty(t, in)
					require.Equal(t, tst.want.inMsg.Id(), in.Id())
				}

				// check outgoing message
				if tst.want.outMsg != nil {
					out := o.OutgoingMessage()
					require.NotEmpty(t, out)
					require.Equal(t, tst.want.outMsg.Id(), out.Id())
				}

				// check id
				if tst.want.id != "" {
					require.Equal(t, tst.want.id, o.Id())
				}

				// check errors
				errsList := o.Errors()
				require.Equal(t, len(tst.want.errList), len(errsList))
				if len(tst.want.errList) > 0 {
					require.True(t,
						reflect.DeepEqual(errsList, tst.want.errList))
				}

				// check implementation
				impl := o.Implementation()
				if tst.args.executor != nil {
					res, err := impl.Execute(o)
					t.Log(impl.Type())
					t.Logf("operation executed with %v: %v", res, err)
				}
			})
	}
}
