package events

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// dataParam builds a parameter over item id carrying val in the state st.
func dataParam(
	t *testing.T,
	name, id string,
	val any,
	st *data.SrcState,
) *data.Parameter {
	t.Helper()

	_ = data.CreateDefaultStates()

	if st == nil {
		st = data.ReadyDataState
	}

	p, err := data.NewParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(val),
				foundation.WithID(id)),
			st))
	require.NoError(t, err)

	return p
}

// msgDef builds a message definition whose payload item has id and carries
// val — the item-bearing definition an event's data parameter pairs with.
func msgDef(t *testing.T, id string, val any) *MessageEventDefinition {
	t.Helper()

	return MustMessageEventDefinition(
		bpmncommon.MustMessage("msg-"+id,
			data.MustItemDefinition(values.NewVariable(val),
				foundation.WithID(id))),
		nil)
}

// bareMsgDef builds a message definition over an item with no structure.
func bareMsgDef(t *testing.T, id string) *MessageEventDefinition {
	t.Helper()

	return MustMessageEventDefinition(
		bpmncommon.MustMessage("msg-"+id,
			data.MustItemDefinition(nil, foundation.WithID(id))),
		nil)
}

// frameFor builds a fresh plane + frame for node nodeID.
func frameFor(t *testing.T, nodeID string) *scope.Frame {
	t.Helper()

	pl, err := scope.New(scope.RootDataPath, nil)
	require.NoError(t, err)

	f, err := scope.NewFrame("track-e", nodeID, pl.Root(), pl)
	require.NoError(t, err)

	return f
}

// inputAssoc builds an input association onto the item-1 target from a
// source over srcID, in the given states.
func inputAssoc(
	t *testing.T, targetID, srcID string, targetSt, srcSt *data.SrcState,
) *data.Association {
	t.Helper()

	target := data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID(targetID)),
		targetSt)

	ia, err := data.NewAssociation(
		target,
		data.WithSource(
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID(srcID)),
				srcSt)))
	require.NoError(t, err)

	return ia
}

// TestThrowEventLoadData covers the throw-side consumer role (SRD-007
// FR-6): input/property instantiation in the frame and the association
// fill of the frame instances, including the Ready flip. The inputs are
// declared and the associations bound through the public surface
// (SRD-094 T-18).
func TestThrowEventLoadData(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	newThrow := func(t *testing.T) *throwEvent {
		te, err := newThrowEvent("thr",
			[]*data.Property{
				data.MustProperty("cnt",
					data.MustItemDefinition(values.NewVariable(7)),
					data.ReadyDataState),
			},
			[]flow.EventDefinition{msgDef(t, "item-1", "")},
			WithDataInputs(
				dataParam(t, "in-1", "item-1", "", data.UnavailableDataState)))
		require.NoError(t, err)

		return te
	}

	t.Run("inputs and properties instantiate; associations fill",
		func(t *testing.T) {
			te := newThrow(t)

			ia := inputAssoc(t, "item-1", "src-1", nil, nil)

			// the upstream producer primes the association (UpdateSource
			// fills the source and flips the target Ready — the IsReady
			// handshake).
			require.NoError(t, ia.UpdateSource(ctx,
				data.MustItemDefinition(values.NewVariable("hello"),
					foundation.WithID("src-1")),
				data.Recalculate))

			require.NoError(t, te.BindIncoming(ia))

			f := frameFor(t, te.ID())
			require.NoError(t, te.LoadData(ctx, f))

			// the frame instance is filled AND flipped to Ready.
			d, err := f.GetDataByID("item-1")
			require.NoError(t, err)
			require.Equal(t, "hello", d.Value().Get(ctx))
			require.Equal(t, data.ReadyDataState.Name(), d.State().Name())

			// the property is in the frame too.
			p, err := f.GetData("cnt")
			require.NoError(t, err)
			require.Equal(t, 7, p.Value().Get(ctx))

			// the DEFINITION input stays untouched (per-frame instances).
			require.Equal(t, "", te.dataInputs[0].Value().Get(ctx))
		})

	t.Run("not-ready association fails", func(t *testing.T) {
		te := newThrow(t)

		require.NoError(t, te.BindIncoming(inputAssoc(t, "item-1", "src-1",
			data.UnavailableDataState, data.UnavailableDataState)))

		require.Error(t, te.LoadData(ctx, frameFor(t, te.ID())))
	})

	t.Run("an input over a structure-less item is refused at construction",
		func(t *testing.T) {
			// the definition's item carries no value, so it is not
			// item-bearing (p217: the payload does not flow) — a parameter
			// declared for it pairs with nothing
			_, err := newThrowEvent("thr-bad-in", nil,
				[]flow.EventDefinition{bareMsgDef(t, "bad")},
				WithDataInputs(data.MustParameter("bad-in",
					data.MustItemAwareElement(
						data.MustItemDefinition(nil, foundation.WithID("bad")),
						data.ReadyDataState))))
			require.ErrorContains(t, err, "a parameter nothing fills")
		})

	t.Run("failing association evaluation is reported", func(t *testing.T) {
		te := newThrow(t)

		// the target claims Ready, but the association can't calculate:
		// its source is unavailable.
		target := data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("item-1")),
			nil)

		ia, err := data.NewAssociation(
			target,
			data.WithSource(
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(""),
						foundation.WithID("src-na")),
					data.UnavailableDataState)))
		require.NoError(t, err)

		require.NoError(t, target.UpdateState(data.ReadyDataState))
		require.NoError(t, te.BindIncoming(ia))

		require.Error(t, te.LoadData(ctx, frameFor(t, te.ID())))
	})

	t.Run("association without a matching input fails", func(t *testing.T) {
		te := newThrow(t)

		target := data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(1),
				foundation.WithID("alien")),
			nil)

		ia, err := data.NewAssociation(
			target,
			data.WithSource(
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(1),
						foundation.WithID("src-2")),
					nil)))
		require.NoError(t, err)

		require.NoError(t, ia.UpdateSource(ctx,
			data.MustItemDefinition(values.NewVariable(1),
				foundation.WithID("src-2")),
			data.Recalculate))

		require.NoError(t, te.BindIncoming(ia))

		require.Error(t, te.LoadData(ctx, frameFor(t, te.ID())))
	})
}

// TestThrowEventStartGate covers the SRD-009 start-gate in throwEvent.LoadData:
// a required input that can't be filled fails fast (gobpm never waits for data,
// ADR-011 v.2 §2.3), while an optional input may stay unavailable.
func TestThrowEventStartGate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("optional input with a not-ready association is allowed",
		func(t *testing.T) {
			te, err := newThrowEvent("thr-opt", nil,
				[]flow.EventDefinition{msgDef(t, "opt-1", "")},
				WithDataInputs(data.MustParameter("opt-in",
					data.MustItemAwareElement(
						data.MustItemDefinition(values.NewVariable(""),
							foundation.WithID("opt-1")),
						data.UnavailableDataState), data.Optional())))
			require.NoError(t, err)

			require.NoError(t, te.BindIncoming(inputAssoc(t, "opt-1", "src-x",
				data.UnavailableDataState, data.UnavailableDataState)))

			require.NoError(t, te.LoadData(ctx, frameFor(t, te.ID())))
		})

	t.Run("required input with no association fails",
		func(t *testing.T) {
			te, err := newThrowEvent("thr-req", nil,
				[]flow.EventDefinition{msgDef(t, "req-1", "")},
				WithDataInputs(
					dataParam(t, "req-in", "req-1", "", data.UnavailableDataState)))
			require.NoError(t, err)

			require.Error(t, te.LoadData(ctx, frameFor(t, te.ID())))
		})
}

// TestCatchEventUploadDataBranches covers the catch-side producer role
// branches: the association push from frame output instances, the
// not-Ready guard, and the missing-output guard (SRD-007 FR-6).
func TestCatchEventUploadDataBranches(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	newCatch := func(t *testing.T, st *data.SrcState) *catchEvent {
		ce, err := newCatchEvent("cth", nil,
			[]flow.EventDefinition{msgDef(t, "item-1", "caught")}, false,
			WithDataOutputs(dataParam(t, "out-1", "item-1", "caught", st)))
		require.NoError(t, err)

		return ce
	}

	bindTarget := func(t *testing.T, ce *catchEvent, srcID string) *data.Association {
		t.Helper()

		target := data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("sink")),
			data.UnavailableDataState)

		oa, err := data.NewAssociation(
			target,
			data.WithSource(
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(""),
						foundation.WithID(srcID)),
					data.UnavailableDataState)))
		require.NoError(t, err)

		require.NoError(t, ce.BindOutgoing(oa))

		return oa
	}

	t.Run("push from the frame output instance", func(t *testing.T) {
		ce := newCatch(t, data.ReadyDataState)
		oa := bindTarget(t, ce, "item-1")

		require.NoError(t, ce.UploadData(ctx, frameFor(t, ce.ID())))

		// UpdateSource(NoRecalculate) primes the association's source; the
		// value flows to the consumer at evaluation time.
		v, err := oa.Value(ctx)
		require.NoError(t, err)
		require.Equal(t, "caught", v.Structure().Get(ctx))
	})

	t.Run("not-Ready output is rejected", func(t *testing.T) {
		ce := newCatch(t, data.UnavailableDataState)
		bindTarget(t, ce, "item-1")

		require.Error(t, ce.UploadData(ctx, frameFor(t, ce.ID())))
	})

	t.Run("association source without an output is rejected",
		func(t *testing.T) {
			ce := newCatch(t, data.ReadyDataState)
			bindTarget(t, ce, "alien")

			require.Error(t, ce.UploadData(ctx, frameFor(t, ce.ID())))
		})

	t.Run("type-mismatched association source fails the push",
		func(t *testing.T) {
			ce := newCatch(t, data.ReadyDataState) // output value: string

			// the association source is an INT variable with the output's
			// item id — UpdateSource's value copy must reject the string.
			target := data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("sink2")),
				data.UnavailableDataState)

			oa, err := data.NewAssociation(
				target,
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(values.NewVariable(0),
							foundation.WithID("item-1")),
						data.UnavailableDataState)))
			require.NoError(t, err)

			require.NoError(t, ce.BindOutgoing(oa))

			require.Error(t, ce.UploadData(ctx, frameFor(t, ce.ID())))
		})

	t.Run("an output over a structure-less item is refused at construction",
		func(t *testing.T) {
			_, err := newCatchEvent("cth-bad", nil,
				[]flow.EventDefinition{bareMsgDef(t, "bad")}, false,
				WithDataOutputs(data.MustParameter("bad-out",
					data.MustItemAwareElement(
						data.MustItemDefinition(nil, foundation.WithID("bad")),
						data.ReadyDataState))))
			require.ErrorContains(t, err, "a parameter nothing fills")
		})

	t.Run("a structure-less payload declares no output", func(t *testing.T) {
		ce, err := newCatchEvent("cth-bare", nil,
			[]flow.EventDefinition{bareMsgDef(t, "bare")}, false)
		require.NoError(t, err)
		require.Empty(t, ce.Outputs())
		require.NoError(t, ce.UploadData(ctx, frameFor(t, ce.ID())))
	})
}

// TestCatchEventUploadDataLoadsProperties covers FIX-018 3.2.3: catchEvent.
// UploadData materialises the event's properties in the frame, so a catch
// event's declared property is readable during execution (like a throw event's
// and a task's). All catch events (Start, IntermediateCatch, Boundary) share
// this UploadData, so the single test covers the path for every catch kind.
func TestCatchEventUploadDataLoadsProperties(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	ce, err := newCatchEvent("catch",
		[]*data.Property{
			data.MustProperty("cnt",
				data.MustItemDefinition(values.NewVariable(7)),
				data.ReadyDataState),
		},
		nil, false)
	require.NoError(t, err)

	f := frameFor(t, ce.ID())
	require.NoError(t, ce.UploadData(ctx, f))

	p, err := f.GetData("cnt")
	require.NoError(t, err)
	require.Equal(t, 7, p.Value().Get(ctx))
}
