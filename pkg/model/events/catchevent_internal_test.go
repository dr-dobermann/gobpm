package events

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestCatchEventProcessEventCaptures verifies the catch-side capture (SRD-014):
// ProcessEvent stores a fired message definition's payload item (bound into
// scope on resume by an IntermediateCatchEvent), and captures nothing for a
// payload-less trigger. It is an internal test because `received` is unexported.
func TestCatchEventProcessEventCaptures(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("captures a fired message payload",
		func(t *testing.T) {
			se, err := NewStartEvent("start")
			require.NoError(t, err)

			fired := MustMessageEventDefinition(
				bpmncommon.MustMessage("order placed",
					data.MustItemDefinition(values.NewVariable("ORD-9"),
						foundation.WithID("order_item"))),
				nil)

			require.NoError(t, se.ProcessEvent(ctx, fired))
			require.NotNil(t, se.received)
			require.Equal(t, "order_item", se.received.ID())
			require.Equal(t, "ORD-9", se.received.Structure().Get(ctx))
		})

	t.Run("captures nothing for a payload-less trigger",
		func(t *testing.T) {
			se, err := NewStartEvent("start")
			require.NoError(t, err)

			require.NoError(t, se.ProcessEvent(ctx,
				MustSignalEventDefinition(&Signal{})))
			require.Nil(t, se.received)
		})
}

// fakeFrame is a minimal exec.Frame for unit-testing catchEvent.UploadData: it
// keeps the instantiated outputs so the test can read the bound value.
type fakeFrame struct{ outs []*data.Parameter }

func (f *fakeFrame) InstantiateInputs([]*data.Parameter) error    { return nil }
func (f *fakeFrame) InstantiateOutputs(d []*data.Parameter) error { f.outs = d; return nil }
func (f *fakeFrame) LoadProperties([]*data.Property) error        { return nil }
func (f *fakeFrame) Inputs() []*data.Parameter                    { return nil }
func (f *fakeFrame) Outputs() []*data.Parameter                   { return f.outs }
func (f *fakeFrame) GetDataByID(string) (data.Data, error)        { return nil, nil }

// TestCatchEventUploadDataBindsReceived verifies the WS-C3 bind: when a payload
// was captured, UploadData carries the runtime value into the matching output
// (overriding the static value).
func TestCatchEventUploadDataBindsReceived(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	med := MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_item"))),
		nil)

	ice, err := NewIntermediateCatchEvent("catch", med)
	require.NoError(t, err)

	// simulate the waiter capture on resume.
	ice.received = data.MustItemDefinition(values.NewVariable("ORD-9"),
		foundation.WithID("order_item"))

	ff := &fakeFrame{}
	require.NoError(t, ice.UploadData(ctx, ff))

	require.Len(t, ff.outs, 1)
	require.Equal(t, "ORD-9", ff.outs[0].ItemDefinition().Structure().Get(ctx))
}

// TestCatchEventUploadDataBindError covers the bind error path: a captured
// payload whose type doesn't match the output variable fails the update.
func TestCatchEventUploadDataBindError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	med := MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_item"))),
		nil)

	ice, err := NewIntermediateCatchEvent("catch", med)
	require.NoError(t, err)

	// the output item is a string variable; an int payload fails the update.
	ice.received = data.MustItemDefinition(values.NewVariable(42),
		foundation.WithID("order_item"))

	require.Error(t, ice.UploadData(ctx, &fakeFrame{}))
}
