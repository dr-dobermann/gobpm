package activities

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// dataPar builds a Ready parameter over item id carrying val.
func dataPar(t *testing.T, name, id string, val any) *data.Parameter {
	t.Helper()

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(val),
				foundation.WithID(id)),
			data.ReadyDataState))
}

// newFrameFor builds a fresh data plane and a frame on its root.
func newFrameFor(t *testing.T, nodeID string) *scope.Frame {
	t.Helper()

	pl, err := scope.New(scope.RootDataPath, nil)
	require.NoError(t, err)

	f, err := scope.NewFrame("track-t", nodeID, pl.Root(), pl)
	require.NoError(t, err)

	return f
}

// newIOTask builds a task with one input (item id inID) and one output
// (item id outID) — distinct ids, the realistic BPMN shape.
func newIOTask(t *testing.T, inID, outID string, inVal, outVal any) *task {
	t.Helper()

	tsk, err := newTask("io-task",
		WithSet("ins", "", data.Input, data.DefaultSet,
			[]*data.Parameter{data.MustParameter("in param",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(inVal),
						foundation.WithID(inID)),
					data.ReadyDataState))}),
		WithSet("outs", "", data.Output, data.DefaultSet,
			[]*data.Parameter{data.MustParameter("out param",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(outVal),
						foundation.WithID(outID)),
					nil))}))
	require.NoError(t, err)

	return tsk
}

// TestTaskDataErrorPaths covers the consumer/producer failure branches of
// the frame-based task data flow (SRD-007 FR-5).
func TestTaskDataErrorPaths(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("association without a matching input fails LoadData",
		func(t *testing.T) {
			tsk := newIOTask(t, "in-x", "out-x", 1, 0)

			// the association targets item id "other" — no such input.
			ghost, err := dataobjects.New("ghost",
				data.MustItemDefinition(values.NewVariable(5),
					foundation.WithID("other")),
				data.ReadyDataState)
			require.NoError(t, err)

			other := newIOTask(t, "other", "other-out", 0, 0)
			require.NoError(t, ghost.AssociateTarget(other, nil))

			// steal the association into tsk to force the mismatch.
			tsk.dataAssociations[data.Input] =
				other.dataAssociations[data.Input]

			require.Error(t, tsk.LoadData(ctx, newFrameFor(t, tsk.ID())))
		})

	t.Run("double instantiation fails (sealed-frame style reuse)",
		func(t *testing.T) {
			tsk := newIOTask(t, "in-x", "out-x", 1, 0)
			f := newFrameFor(t, tsk.ID())

			require.NoError(t, tsk.LoadData(ctx, f))
			require.Error(t, tsk.LoadData(ctx, f),
				"duplicate instantiation must be rejected")
		})

	t.Run("not-Ready output with no produced data fails UploadData",
		func(t *testing.T) {
			tsk := newIOTask(t, "in-x", "out-x", 1, 0)
			f := newFrameFor(t, tsk.ID())

			// nothing produced data for "out-x": the not-Ready output can't
			// be filled — outputs are write targets and never self-resolve.
			require.NoError(t, tsk.LoadData(ctx, f))
			require.Error(t, tsk.UploadData(ctx, f))
		})

	t.Run("output association without matching output fails UploadData",
		func(t *testing.T) {
			tsk := newIOTask(t, "in-x", "out-x", 1, 0)
			f := newFrameFor(t, tsk.ID())

			require.NoError(t, tsk.LoadData(ctx, f))

			// make the output instance fillable so updateOutputs passes...
			require.NoError(t, f.Put(data.MustParameter("res",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(7),
						foundation.WithID("out-x")),
					data.ReadyDataState))))

			// ...and bind an outgoing association whose source id matches
			// nothing the task outputs.
			alien := newIOTask(t, "alien-in", "alien-out", 0, 0)
			out, err := dataobjects.New("sink",
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID("alien-out")),
				nil)
			require.NoError(t, err)
			require.NoError(t,
				out.AssociateSource(alien, []string{"alien-out"}, nil))

			tsk.dataAssociations[data.Output] =
				alien.dataAssociations[data.Output]

			require.Error(t, tsk.UploadData(ctx, f))
		})

	t.Run("under-specified definitions fail instantiation",
		func(t *testing.T) {
			// an ItemDefinition without a value can't produce a frame
			// instance — each definition group reports its own wrap.
			bare := func(name, id string) *data.Parameter {
				return data.MustParameter(name,
					data.MustItemAwareElement(
						data.MustItemDefinition(nil, foundation.WithID(id)),
						data.ReadyDataState))
			}

			badIn, err := newTask("bad-in",
				WithSet("ins", "", data.Input, data.DefaultSet,
					[]*data.Parameter{bare("in", "bad-in-id")}),
				WithSet("outs", "", data.Output, data.DefaultSet,
					[]*data.Parameter{dataPar(t, "out", "ok-out", 0)}))
			require.NoError(t, err)
			require.Error(t, badIn.LoadData(ctx, newFrameFor(t, badIn.ID())))

			badOut, err := newTask("bad-out",
				WithSet("ins", "", data.Input, data.DefaultSet,
					[]*data.Parameter{dataPar(t, "in", "ok-in", 0)}),
				WithSet("outs", "", data.Output, data.DefaultSet,
					[]*data.Parameter{bare("out", "bad-out-id")}))
			require.NoError(t, err)
			require.Error(t, badOut.LoadData(ctx, newFrameFor(t, badOut.ID())))

			badProp, err := newTask("bad-prop",
				data.WithProperties(data.MustProperty("p",
					data.MustItemDefinition(nil), data.ReadyDataState)),
				WithSet("ins", "", data.Input, data.DefaultSet,
					[]*data.Parameter{dataPar(t, "in", "ok-in2", 0)}),
				WithSet("outs", "", data.Output, data.DefaultSet,
					[]*data.Parameter{dataPar(t, "out", "ok-out2", 0)}))
			require.NoError(t, err)
			require.Error(t,
				badProp.LoadData(ctx, newFrameFor(t, badProp.ID())))
		})

	t.Run("not-Ready produced data is rejected by updateOutputs",
		func(t *testing.T) {
			tsk := newIOTask(t, "in-x", "out-x", 1, 0)
			f := newFrameFor(t, tsk.ID())

			require.NoError(t, tsk.LoadData(ctx, f))

			// the produced data matches the output id but is NOT Ready.
			require.NoError(t, f.Put(data.MustParameter("res",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(7),
						foundation.WithID("out-x")),
					data.UnavailableDataState))))

			require.Error(t, tsk.UploadData(ctx, f))
		})

	t.Run("produced data drives the output to the association target",
		func(t *testing.T) {
			tsk := newIOTask(t, "in-x", "out-x", 1, 0)
			f := newFrameFor(t, tsk.ID())

			sink, err := dataobjects.New("sink",
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID("out-x")),
				nil)
			require.NoError(t, err)
			require.NoError(t,
				sink.AssociateSource(tsk, []string{"out-x"}, nil))

			require.NoError(t, tsk.LoadData(ctx, f))

			// the node produces a Ready result (the UserTask/ServiceTask
			// path) — updateOutputs copies it into the output instance.
			require.NoError(t, f.Put(data.MustParameter("res",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(99),
						foundation.WithID("out-x")),
					data.ReadyDataState))))

			require.NoError(t, tsk.UploadData(ctx, f))
			require.Equal(t, 99, sink.Subject().Structure().Get(ctx))
		})
}
