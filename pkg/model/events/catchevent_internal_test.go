package events

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestCatchEventProcessEventIsStateless pins the SRD-085 FR-1 contract:
// the node's delivery notification stores NOTHING — a node is a
// runtime-immutable definition shared by every execution of its
// instance, and the payload rides the receiving execution's frame
// instead (ADR-006 v.5 §2.9.1).
func TestCatchEventProcessEventIsStateless(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	se, err := NewStartEvent("start")
	require.NoError(t, err)

	fired := MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable("ORD-9"),
				foundation.WithID("order_item"))),
		nil)

	require.NoError(t, se.ProcessEvent(ctx, fired))
	require.NoError(t, se.ProcessEvent(ctx,
		MustSignalEventDefinition(&Signal{})))
}

// fakeFrame is a minimal exec.Frame for unit-testing catchEvent.UploadData: it
// keeps the instantiated outputs so the test can read the bound value, and
// carries the delivery payload the way the real frame does (SRD-085 FR-1).
type fakeFrame struct {
	outs     []*data.Parameter
	received *data.ItemDefinition
}

func (f *fakeFrame) InstantiateInputs([]*data.Parameter) error    { return nil }
func (f *fakeFrame) InstantiateOutputs(d []*data.Parameter) error { f.outs = d; return nil }
func (f *fakeFrame) LoadProperties([]*data.Property) error        { return nil }
func (f *fakeFrame) Inputs() []*data.Parameter                    { return nil }
func (f *fakeFrame) Outputs() []*data.Parameter                   { return f.outs }
func (f *fakeFrame) GetDataByID(string) (data.Data, error)        { return nil, nil }
func (f *fakeFrame) GetData(string) (data.Data, error)            { return nil, nil }
func (f *fakeFrame) DataStores() datastore.Registry               { return nil }
func (f *fakeFrame) ExpressionEngine() expression.Engine          { return nil }
func (f *fakeFrame) RecordDataMovement(_, _ bool, _, _ string)    {}
func (f *fakeFrame) SetReceived(i *data.ItemDefinition)           { f.received = i }
func (f *fakeFrame) Received() *data.ItemDefinition               { return f.received }

// TestCatchEventUploadDataBindsReceived verifies the WS-C3 bind over the
// SRD-085 frame carrier: a payload staged on THIS execution's frame is
// carried into the matching output (overriding the static value).
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

	// the receiving execution staged its delivery's payload here.
	ff := &fakeFrame{}
	ff.SetReceived(data.MustItemDefinition(values.NewVariable("ORD-9"),
		foundation.WithID("order_item")))

	require.NoError(t, ice.UploadData(ctx, ff))

	require.Len(t, ff.outs, 1)
	require.Equal(t, "ORD-9", ff.outs[0].ItemDefinition().Structure().Get(ctx))
}

// TestCatchEventUploadDataBindError covers the bind error path: a staged
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

	ff := &fakeFrame{}
	ff.SetReceived(data.MustItemDefinition(values.NewVariable(42),
		foundation.WithID("order_item")))

	require.Error(t, ice.UploadData(ctx, ff))
}

// TestWithIterationCorrelation pins the SRD-085 FR-3 option: parameter
// validation, the applied pair, the accessor's empty answer, and the
// declaration surviving a per-instance clone.
func TestWithIterationCorrelation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	expr := data.FormalExpression(nil)

	stub := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable("v"), nil
		})

	med := MustMessageEventDefinition(
		bpmncommon.MustMessage("m", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("m_in"))), nil)

	t.Run("an empty key name is refused", func(t *testing.T) {
		_, err := NewIntermediateCatchEvent("c", med,
			WithIterationCorrelation("  ", stub))
		require.ErrorContains(t, err, "key name isn't allowed")
	})

	t.Run("a nil expression is refused", func(t *testing.T) {
		_, err := NewIntermediateCatchEvent("c", med,
			WithIterationCorrelation("k", expr))
		require.ErrorContains(t, err, "nil expression")
	})

	t.Run("the declared pair survives the clone", func(t *testing.T) {
		ice, err := NewIntermediateCatchEvent("c", med,
			WithIterationCorrelation("k", stub))
		require.NoError(t, err)

		name, e := ice.IterationCorrelation()
		require.Equal(t, "k", name)
		require.NotNil(t, e)

		cl, err := ice.catchEvent.clone()
		require.NoError(t, err)

		name, e = cl.IterationCorrelation()
		require.Equal(t, "k", name)
		require.NotNil(t, e)
	})

	t.Run("no declaration answers empty", func(t *testing.T) {
		ice, err := NewIntermediateCatchEvent("c", med)
		require.NoError(t, err)

		name, e := ice.IterationCorrelation()
		require.Empty(t, name)
		require.Nil(t, e)
	})
}
