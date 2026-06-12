package events

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
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

// frameFor builds a fresh plane + frame for node nodeID.
func frameFor(t *testing.T, nodeID string) *scope.Frame {
	t.Helper()

	pl, err := scope.New(scope.RootDataPath, nil)
	require.NoError(t, err)

	f, err := scope.NewFrame("track-e", nodeID, pl.Root(), pl)
	require.NoError(t, err)

	return f
}

// TestThrowEventLoadData covers the throw-side consumer role (SRD-007
// FR-6): input/property instantiation in the frame and the association
// fill of the frame instances, including the Ready flip.
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
			nil)
		require.NoError(t, err)

		te.dataInputs["item-1"] =
			dataParam(t, "in-1", "item-1", "", data.UnavailableDataState)

		return te
	}

	t.Run("inputs and properties instantiate; associations fill",
		func(t *testing.T) {
			te := newThrow(t)

			target := data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("item-1")),
				nil)

			ia, err := data.NewAssociation(
				target,
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(values.NewVariable(""),
							foundation.WithID("src-1")),
						nil)))
			require.NoError(t, err)

			// the upstream producer primes the association (UpdateSource
			// fills the source and flips the target Ready — the IsReady
			// handshake).
			require.NoError(t, ia.UpdateSource(ctx,
				data.MustItemDefinition(values.NewVariable("hello"),
					foundation.WithID("src-1")),
				data.Recalculate))

			te.inputAssociations = append(te.inputAssociations, ia)

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
			require.Equal(t, "",
				te.dataInputs["item-1"].Value().Get(ctx))
		})

	t.Run("not-ready association fails", func(t *testing.T) {
		te := newThrow(t)

		target := data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("item-1")),
			data.UnavailableDataState)

		ia, err := data.NewAssociation(
			target,
			data.WithSource(
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(""),
						foundation.WithID("src-1")),
					data.UnavailableDataState)))
		require.NoError(t, err)

		te.inputAssociations = append(te.inputAssociations, ia)

		require.Error(t, te.LoadData(ctx, frameFor(t, te.ID())))
	})

	t.Run("under-specified input definition fails instantiation",
		func(t *testing.T) {
			te, err := newThrowEvent("thr-bad-in", nil, nil)
			require.NoError(t, err)

			te.dataInputs["bad"] = data.MustParameter("bad-in",
				data.MustItemAwareElement(
					data.MustItemDefinition(nil, foundation.WithID("bad")),
					data.ReadyDataState))

			require.Error(t, te.LoadData(ctx, frameFor(t, te.ID())))
		})

	t.Run("under-specified property fails loading", func(t *testing.T) {
		te, err := newThrowEvent("thr-bad-prop",
			[]*data.Property{
				data.MustProperty("bad-prop",
					data.MustItemDefinition(nil),
					data.ReadyDataState),
			},
			nil)
		require.NoError(t, err)

		require.Error(t, te.LoadData(ctx, frameFor(t, te.ID())))
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

		te.inputAssociations = append(te.inputAssociations, ia)

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

		te.inputAssociations = append(te.inputAssociations, ia)

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
		ce, err := newCatchEvent("cth", nil, nil, false)
		require.NoError(t, err)

		ce.dataOutputs["item-1"] =
			dataParam(t, "out-1", "item-1", "caught", st)

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

		ce.outputAssociations = append(ce.outputAssociations, oa)

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

			ce.outputAssociations = append(ce.outputAssociations, oa)

			require.Error(t, ce.UploadData(ctx, frameFor(t, ce.ID())))
		})

	t.Run("under-specified output definition fails instantiation",
		func(t *testing.T) {
			ce, err := newCatchEvent("cth-bad", nil, nil, false)
			require.NoError(t, err)

			ce.dataOutputs["bad"] = data.MustParameter("bad-out",
				data.MustItemAwareElement(
					data.MustItemDefinition(nil, foundation.WithID("bad")),
					data.ReadyDataState))

			require.Error(t, ce.UploadData(ctx, frameFor(t, ce.ID())))
		})
}
