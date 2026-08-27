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

// scopeDatum builds a datum named name carrying val in state st — what a
// data object is in a per-instance scope.
func scopeDatum(t *testing.T, name string, val any, st *data.SrcState) data.Data {
	t.Helper()

	return dataParam(t, name, name, val, st)
}

// frameFor builds a fresh plane seeded with dd and a frame for node nodeID.
func frameFor(t *testing.T, nodeID string, dd ...data.Data) *scope.Frame {
	t.Helper()

	pl, err := scope.New(scope.RootDataPath, nil)
	require.NoError(t, err)

	if len(dd) > 0 {
		_, err = pl.Commit(pl.Root(), dd...)
		require.NoError(t, err)
	}

	f, err := scope.NewFrame("track-e", nodeID, pl.Root(), pl)
	require.NoError(t, err)

	return f
}

// inputAssoc builds an input association from the scope datum named src
// onto the event input over item targetID.
func inputAssoc(t *testing.T, src, targetID string) *data.Association {
	t.Helper()

	ia, err := data.NewAssociation(
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID(targetID)),
			nil),
		data.WithSource(
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID(src)),
				nil)))
	require.NoError(t, err)

	return ia
}

// outputAssoc builds an output association from the event output over item
// srcID onto the scope datum named sink.
func outputAssoc(t *testing.T, srcID, sink string) *data.Association {
	t.Helper()

	oa, err := data.NewAssociation(
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID(sink)),
			data.UnavailableDataState),
		data.WithSource(
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID(srcID)),
				data.UnavailableDataState)))
	require.NoError(t, err)

	return oa
}

// TestThrowEventLoadData covers the throw-side consumer role (SRD-007
// FR-6): input/property instantiation in the frame and the association
// fill of the frame instances from the per-instance scope (SRD-094 FR-3),
// including the Ready flip. The inputs are declared and the associations
// bound through the public surface (T-18).
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

	t.Run("inputs and properties instantiate; associations fill from scope",
		func(t *testing.T) {
			te := newThrow(t)
			require.NoError(t, te.BindIncoming(inputAssoc(t, "src-1", "item-1")))

			f := frameFor(t, te.ID(),
				scopeDatum(t, "src-1", "hello", data.ReadyDataState))
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

	t.Run("a not-Ready source fails a required input", func(t *testing.T) {
		te := newThrow(t)
		require.NoError(t, te.BindIncoming(inputAssoc(t, "src-1", "item-1")))

		require.Error(t, te.LoadData(ctx, frameFor(t, te.ID(),
			scopeDatum(t, "src-1", "", data.UnavailableDataState))))
	})

	t.Run("a source missing from scope fails a required input",
		func(t *testing.T) {
			te := newThrow(t)
			require.NoError(t, te.BindIncoming(inputAssoc(t, "src-na", "item-1")))

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

	t.Run("association without a matching input fails", func(t *testing.T) {
		te := newThrow(t)
		require.NoError(t, te.BindIncoming(inputAssoc(t, "src-2", "alien")))

		require.Error(t, te.LoadData(ctx, frameFor(t, te.ID(),
			scopeDatum(t, "src-2", "", data.ReadyDataState))))
	})
}

// TestThrowEventStartGate covers the SRD-009 start-gate in throwEvent.LoadData:
// a required input that can't be filled fails fast (gobpm never waits for data,
// ADR-011 v.2 §2.3), while an optional input may stay unavailable.
func TestThrowEventStartGate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("optional input with a not-ready source is allowed",
		func(t *testing.T) {
			te, err := newThrowEvent("thr-opt", nil,
				[]flow.EventDefinition{msgDef(t, "opt-1", "")},
				WithDataInputs(data.MustParameter("opt-in",
					data.MustItemAwareElement(
						data.MustItemDefinition(values.NewVariable(""),
							foundation.WithID("opt-1")),
						data.UnavailableDataState), data.Optional())))
			require.NoError(t, err)
			require.NoError(t, te.BindIncoming(inputAssoc(t, "src-x", "opt-1")))

			require.NoError(t, te.LoadData(ctx, frameFor(t, te.ID(),
				scopeDatum(t, "src-x", "", data.UnavailableDataState))))
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
// branches: the association push into the per-instance scope datum
// (SRD-094 FR-3), the not-Ready skip, and the missing-output guard.
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

	t.Run("push lands in the frame's scope datum", func(t *testing.T) {
		ce := newCatch(t, data.ReadyDataState)
		require.NoError(t, ce.BindOutgoing(outputAssoc(t, "item-1", "sink")))

		f := frameFor(t, ce.ID(), scopeDatum(t, "sink", "", data.ReadyDataState))
		require.NoError(t, ce.UploadData(ctx, f))

		d, err := f.GetData("sink")
		require.NoError(t, err)
		require.Equal(t, "caught", d.Value().Get(ctx))
	})

	t.Run("a not-Ready output pushes nothing", func(t *testing.T) {
		ce := newCatch(t, data.UnavailableDataState)
		require.NoError(t, ce.BindOutgoing(outputAssoc(t, "item-1", "sink")))

		f := frameFor(t, ce.ID(), scopeDatum(t, "sink", "before", data.ReadyDataState))
		require.NoError(t, ce.UploadData(ctx, f))

		d, err := f.GetData("sink")
		require.NoError(t, err)
		require.Equal(t, "before", d.Value().Get(ctx))
	})

	t.Run("association source without an output is rejected",
		func(t *testing.T) {
			ce := newCatch(t, data.ReadyDataState)
			require.NoError(t, ce.BindOutgoing(outputAssoc(t, "alien", "sink")))

			require.Error(t, ce.UploadData(ctx, frameFor(t, ce.ID(),
				scopeDatum(t, "sink", "", data.ReadyDataState))))
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

// TestEventCopyPathIsPerInstance — SRD-094 T-6/T-7, NFR-2: one catch
// event and one throw event shared by two planes (two instances of one
// snapshot); the values never cross, and the association's own elements
// — model objects — stay untouched.
func TestEventCopyPathIsPerInstance(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("a catch pushes into each frame's own sink", func(t *testing.T) {
		ce, err := newCatchEvent("cth", nil,
			[]flow.EventDefinition{msgDef(t, "item-1", "")}, false)
		require.NoError(t, err)

		oa := outputAssoc(t, "item-1", "sink")
		require.NoError(t, ce.BindOutgoing(oa))

		fa := frameFor(t, ce.ID(), scopeDatum(t, "sink", "", data.ReadyDataState))
		fb := frameFor(t, ce.ID(), scopeDatum(t, "sink", "", data.ReadyDataState))

		// each delivery stages its own payload on its frame
		fa.SetReceived(data.MustItemDefinition(values.NewVariable("A"),
			foundation.WithID("item-1")))
		fb.SetReceived(data.MustItemDefinition(values.NewVariable("B"),
			foundation.WithID("item-1")))

		require.NoError(t, ce.UploadData(ctx, fa))
		require.NoError(t, ce.UploadData(ctx, fb))

		da, err := fa.GetData("sink")
		require.NoError(t, err)
		db, err := fb.GetData("sink")
		require.NoError(t, err)
		require.Equal(t, "A", da.Value().Get(ctx))
		require.Equal(t, "B", db.Value().Get(ctx))

		// the association's own target — a model object — never saw a
		// value: the copy path reads the association for routing only
		require.False(t, oa.IsReady(),
			"the association's model-side target was written at run time")
	})

	t.Run("a throw fills each frame's own input from its own scope",
		func(t *testing.T) {
			te, err := newThrowEvent("thr", nil,
				[]flow.EventDefinition{msgDef(t, "item-1", "")},
				WithDataInputs(
					dataParam(t, "in-1", "item-1", "", data.UnavailableDataState)))
			require.NoError(t, err)

			ia := inputAssoc(t, "src", "item-1")
			require.NoError(t, te.BindIncoming(ia))

			fa := frameFor(t, te.ID(), scopeDatum(t, "src", "A", data.ReadyDataState))
			fb := frameFor(t, te.ID(), scopeDatum(t, "src", "B", data.ReadyDataState))

			require.NoError(t, te.LoadData(ctx, fa))
			require.NoError(t, te.LoadData(ctx, fb))

			da, err := fa.GetDataByID("item-1")
			require.NoError(t, err)
			db, err := fb.GetDataByID("item-1")
			require.NoError(t, err)
			require.Equal(t, "A", da.Value().Get(ctx))
			require.Equal(t, "B", db.Value().Get(ctx))

			// the definition input and the association stay untouched
			require.Equal(t, "", te.dataInputs[0].Value().Get(ctx))
			require.False(t, ia.IsReady())
		})
}
