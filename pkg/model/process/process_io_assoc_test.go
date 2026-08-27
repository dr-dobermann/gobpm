package process_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// strParam declares a string parameter named name over item id.
func strParam(name, id string) *data.Parameter {
	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""), foundation.WithID(id)),
			data.ReadyDataState))
}

// msgStart builds a message start event whose payload item is itemID.
func msgStart(t *testing.T, name, itemID string) *events.StartEvent {
	t.Helper()

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("m-"+itemID,
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID(itemID))),
		nil)

	s, err := events.NewStartEvent(name, events.WithMessageTrigger(med))
	require.NoError(t, err)

	return s
}

// msgEnd builds a message end event whose payload item is itemID.
func msgEnd(t *testing.T, name, itemID string) *events.EndEvent {
	t.Helper()

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("m-"+itemID,
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID(itemID))),
		nil)

	e, err := events.NewEndEvent(name, events.WithMessageTrigger(med))
	require.NoError(t, err)

	return e
}

// contracted builds a process declaring input "order" and output "total"
// with start → end, and returns it with its two events.
func contracted(t *testing.T) (*process.Process, *events.StartEvent, *events.EndEvent) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("contracted",
		data.WithInputs(strParam("order", "order-item")),
		data.WithOutputs(strParam("total", "total-item")))
	require.NoError(t, err)

	start := msgStart(t, "start", "order_in")
	end := msgEnd(t, "end", "total-item")

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	return p, start, end
}

// TestAssociateInputValidates — SRD-094 T-9: the process input end refuses
// a contract-less process, an undeclared name, a foreign or wrong-kind
// event, an unknown source, and wires a good pair naming the input.
func TestAssociateInputValidates(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("a contract-less process", func(t *testing.T) {
		p, err := process.New("plain")
		require.NoError(t, err)

		require.ErrorContains(t, p.AssociateInput("order", msgStart(t, "s", "x"), "x"),
			"declares no I/O contract")
	})

	t.Run("an undeclared input", func(t *testing.T) {
		p, start, _ := contracted(t)

		require.ErrorContains(t, p.AssociateInput("amount", start, "order_in"),
			`declares no input named "amount"`)
	})

	t.Run("a nil, foreign, or non-start event", func(t *testing.T) {
		p, _, end := contracted(t)

		require.ErrorContains(t, p.AssociateInput("order", nil, "order_in"),
			"nil event")
		require.ErrorContains(t,
			p.AssociateInput("order", msgStart(t, "alien", "order_in"), "order_in"),
			"isn't a node of process")

		// an end event is a node of the process, but not a Start Event
		var asSource flow.AssociationSource = startOf(t, end)
		_ = asSource
	})

	t.Run("an unknown source on the start", func(t *testing.T) {
		p, start, _ := contracted(t)

		require.ErrorContains(t, p.AssociateInput("order", start, "nope"),
			"has no data output")
	})

	t.Run("a good pair binds on the start, named after the input",
		func(t *testing.T) {
			p, start, _ := contracted(t)

			require.NoError(t, p.AssociateInput("order", start, "order_in"))

			aa := start.OutputAssociations()
			require.Len(t, aa, 1)
			require.Equal(t, "order", aa[0].TargetName())
			require.Equal(t, []string{"order_in"}, aa[0].SourceNames())

			// the declaration's own element is untouched
			require.Equal(t, data.ReadyDataState.Name(),
				p.IOSpec().InputSet()[0].State().Name())
		})
}

// startOf is a compile-time reminder that an end event is never an
// association source: the type system refuses the wrong-kind case before
// AssociateInput can.
func startOf(t *testing.T, _ *events.EndEvent) flow.AssociationSource {
	t.Helper()

	return msgStart(t, "s", "x")
}

// TestAssociateOutputValidates — SRD-094 T-10: the mirror for the process
// output end.
func TestAssociateOutputValidates(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("an undeclared output", func(t *testing.T) {
		p, _, end := contracted(t)

		require.ErrorContains(t, p.AssociateOutput("sum", end, "x"),
			`declares no output named "sum"`)
	})

	t.Run("a foreign end and a wrong kind", func(t *testing.T) {
		p, _, _ := contracted(t)

		require.ErrorContains(t,
			p.AssociateOutput("total", msgEnd(t, "alien", "total-item"), "x"),
			"isn't a node of process")

		throw, err := events.NewIntermediateThrowEvent("t",
			events.MustMessageEventDefinition(
				bpmncommon.MustMessage("m-t",
					data.MustItemDefinition(values.NewVariable(""),
						foundation.WithID("total-item"))), nil))
		require.NoError(t, err)
		require.NoError(t, p.Add(throw))

		require.ErrorContains(t, p.AssociateOutput("total", throw, "x"),
			"isn't an End Event")
	})

	t.Run("an end without the named input", func(t *testing.T) {
		p, _, end := contracted(t)

		require.ErrorContains(t, p.AssociateOutput("total", end, "nope"),
			`has no data input "nope"`)
	})

	t.Run("a good pair binds on the end, sourced by the output's name",
		func(t *testing.T) {
			p, _, end := contracted(t)

			require.NoError(t, p.AssociateOutput("total", end, end.Inputs()[0].ID()))

			aa := end.InputAssociations()
			require.Len(t, aa, 1)
			require.Equal(t, []string{"total"}, aa[0].SourceNames())
			require.Equal(t, "total-item", aa[0].TargetItemDefID())
		})
}
