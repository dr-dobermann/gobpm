package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// itemDef builds an item over a string variable with the given id.
func itemDef(id string) *data.ItemDefinition {
	return data.MustItemDefinition(values.NewVariable(""), foundation.WithID(id))
}

// messageDefOver builds a message definition whose payload item is id.
func messageDefOver(t *testing.T, id string) *events.MessageEventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage("m-"+id, itemDef(id)), nil)
}

// signalDefOver builds a signal definition whose payload item is id.
func signalDefOver(t *testing.T, id string) *events.SignalEventDefinition {
	t.Helper()

	sig, err := events.NewSignal("s-"+id, itemDef(id))
	require.NoError(t, err)

	return events.MustSignalEventDefinition(sig)
}

// paramOver declares a parameter named name over item id.
func paramOver(name, id string) *data.Parameter {
	return data.MustParameter(name,
		data.MustItemAwareElement(itemDef(id), data.ReadyDataState))
}

// assocTo builds an association whose target is a fresh sink and whose
// source is the element src.
func assocTo(t *testing.T, sink string, src *data.ItemAwareElement) *data.Association {
	t.Helper()

	a, err := data.NewAssociation(
		data.MustItemAwareElement(itemDef(sink), data.UnavailableDataState),
		data.WithSource(src), foundation.WithID("a-"+sink))
	require.NoError(t, err)

	return a
}

// TestCatchEventIsAssociationSource — SRD-094 T-1: a catch event lists its
// data outputs as association sources and takes an output association,
// refusing nil and a duplicate.
func TestCatchEventIsAssociationSource(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	start, err := events.NewStartEvent("s",
		events.WithMessageTrigger(messageDefOver(t, "order")))
	require.NoError(t, err)

	var src flow.AssociationSource = start

	outs := src.Outputs()
	require.Len(t, outs, 1)
	require.Equal(t, "order", outs[0].ItemDefinition().ID())

	require.ErrorContains(t, src.BindOutgoing(nil), "nil data association")

	a := assocTo(t, "sink", outs[0])
	require.NoError(t, src.BindOutgoing(a))
	require.ErrorContains(t, src.BindOutgoing(a), "already bound")
}

// TestThrowEventIsAssociationTarget — SRD-094 T-2: the mirror for a throw
// event's data inputs.
func TestThrowEventIsAssociationTarget(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	end, err := events.NewEndEvent("e",
		events.WithMessageTrigger(messageDefOver(t, "quote")))
	require.NoError(t, err)

	var trg flow.AssociationTarget = end

	ins := trg.Inputs()
	require.Len(t, ins, 1)
	require.Equal(t, "quote", ins[0].ItemDefinition().ID())

	require.ErrorContains(t, trg.BindIncoming(nil), "nil data association")

	a, err := data.NewAssociation(ins[0],
		data.WithSource(data.MustItemAwareElement(itemDef("total"), nil)))
	require.NoError(t, err)

	require.NoError(t, trg.BindIncoming(a))
	require.ErrorContains(t, trg.BindIncoming(a), "already bound")
}

// TestWithDataOutputsPairsWithDefinitions — SRD-094 T-3: declared outputs
// pair with the item-bearing definitions in order (p217): a matching
// declaration replaces the auto parameter, an item mismatch is refused
// naming both, one past the definitions is refused, nil is refused with
// its index, and an undeclared definition keeps its auto parameter.
func TestWithDataOutputsPairsWithDefinitions(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// a message and a signal trigger, in that order: two item-bearing
	// definitions, two auto outputs
	build := func(catchOpts ...events.CatchOption) (*events.StartEvent, error) {
		opts := []options.Option{
			events.WithMessageTrigger(messageDefOver(t, "order")),
			events.WithSignalTrigger(signalDefOver(t, "alarm")),
		}
		for _, o := range catchOpts {
			opts = append(opts, o)
		}

		return events.NewStartEvent("s", opts...)
	}

	t.Run("a matching declaration replaces the auto parameter, the rest stay",
		func(t *testing.T) {
			declared := paramOver("the order", "order")

			s, err := build(events.WithDataOutputs(declared))
			require.NoError(t, err)

			outs := s.Outputs()
			require.Len(t, outs, 2)
			require.Same(t, &declared.ItemAwareElement, outs[0],
				"the declared parameter took the message's position")
			require.Equal(t, "alarm", outs[1].ItemDefinition().ID())
		})

	t.Run("an item mismatch is refused naming both", func(t *testing.T) {
		_, err := build(events.WithDataOutputs(paramOver("wrong", "alarm")))
		require.ErrorContains(t, err, `carries item "alarm"`)
		require.ErrorContains(t, err, `carries "order"`)
	})

	t.Run("a parameter past the definitions is refused", func(t *testing.T) {
		_, err := build(events.WithDataOutputs(
			paramOver("o", "order"), paramOver("a", "alarm"), paramOver("x", "x")))
		require.ErrorContains(t, err, "a parameter nothing fills")
	})

	t.Run("a nil parameter is refused with its index", func(t *testing.T) {
		_, err := build(events.WithDataOutputs(paramOver("o", "order"), nil))
		require.ErrorContains(t, err, "nil parameter")
		require.ErrorContains(t, err, "1")
	})
}

// TestWithDataInputsOnThrows — SRD-094 T-4: the mirror for throws, and a
// catch whose definitions carry no item takes no parameter at all.
func TestWithDataInputsOnThrows(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("a matching declaration replaces the message's auto input",
		func(t *testing.T) {
			declared := paramOver("the quote", "quote")

			e, err := events.NewEndEvent("e",
				events.WithMessageTrigger(messageDefOver(t, "quote")),
				events.WithDataInputs(declared))
			require.NoError(t, err)

			ins := e.Inputs()
			require.Len(t, ins, 1)
			require.Same(t, &declared.ItemAwareElement, ins[0])
		})

	t.Run("a throw refuses a mismatch too", func(t *testing.T) {
		_, err := events.NewIntermediateThrowEvent("t",
			messageDefOver(t, "quote"),
			events.WithDataInputs(paramOver("wrong", "other")))
		require.ErrorContains(t, err, "p217")
	})

	t.Run("a timer catch carries no item and takes no output", func(t *testing.T) {
		timeExpr, err := goexpr.New(nil,
			data.MustItemDefinition(values.NewVariable(time.Time{})),
			func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable(time.Now().Add(time.Hour)), nil
			})
		require.NoError(t, err)

		timer, err := events.NewTimerEventDefinition(timeExpr, nil, nil)
		require.NoError(t, err)

		_, err = events.NewIntermediateCatchEvent("t", timer,
			events.WithDataOutputs(paramOver("x", "x")))
		require.ErrorContains(t, err, "0 item-bearing definitions")
	})
}

// TestSignalPayloadHasAnOutput — SRD-094 T-5: a signal-triggered catch
// declares the signal item's output the way a message-triggered one does.
func TestSignalPayloadHasAnOutput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	c, err := events.NewIntermediateCatchEvent("c", signalDefOver(t, "alarm"))
	require.NoError(t, err)

	outs := c.Outputs()
	require.Len(t, outs, 1)
	require.Equal(t, "alarm", outs[0].ItemDefinition().ID())
}
