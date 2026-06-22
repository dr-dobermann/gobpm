package instance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// errFailNode is returned by the failing ServiceTask's operation: an undeclared
// execution error (not in the op's WithErrors), so it faults the track.
var errFailNode = errors.New("boom: failing node exec")

// failingSnapshot builds start → ServiceTask(op returns errFailNode) → end. The
// op's error surfaces from run()'s executeNode, leaving the track TrackFailed —
// the FIX-008 canary (a node failure must fault the instance, not complete it).
func failingSnapshot(pname string) (*snapshot.Snapshot, error) {
	p, err := process.New(pname,
		data.WithProperties(
			data.MustProperty(
				"user_name",
				data.MustItemDefinition(
					values.NewVariable("Dr. Dobermann"),
					foundation.WithID("user_name")),
				data.ReadyDataState)))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	op, err := gooper.New(
		"failing op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			return nil, errFailNode
		},
		gooper.WithInMessage(
			bpmncommon.MustMessage(
				"user_name",
				data.MustItemDefinition(
					values.NewVariable(""),
					foundation.WithID("user_name")))),
		gooper.WithErrors(errs.ObjectNotFound, errs.EmptyNotAllowed))
	if err != nil {
		return nil, err
	}

	task, err := activities.NewServiceTask(
		"Failing Task", op, activities.WithoutParams())
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, err
	}

	for _, fe := range []flow.Element{start, task, end} {
		if err := p.Add(fe); err != nil {
			return nil, err
		}
	}

	for _, l := range []struct {
		src flow.SequenceSource
		trg flow.SequenceTarget
	}{
		{start, task},
		{task, end},
	} {
		if _, err := flow.Link(l.src, l.trg); err != nil {
			return nil, err
		}
	}

	return snapshot.New(p)
}

// TestFailedTrackFailsInstance is the FIX-008 canary: a track whose node Exec
// errors must FAIL the instance (Terminated + LastErr surfaced), not silently
// reach Completed with lastErr=nil.
func TestFailedTrackFailsInstance(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := failingSnapshot("fail-track")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := instance.New(
		s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cc := context.WithCancel(context.Background())
	defer cc()
	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("instance did not finish")
	}

	require.Equal(t, instance.Terminated, inst.State(),
		"a failed track must terminate the instance, not complete it")
	require.Error(t, inst.LastErr(),
		"the failed track's error must be surfaced on the instance")
}

// TestNormalCompletionUnaffected confirms the evFailed path doesn't perturb a
// clean run: an ordinary process still reaches Completed with no error.
func TestNormalCompletionUnaffected(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("clean")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := instance.New(
		s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cc := context.WithCancel(context.Background())
	defer cc()
	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("instance did not finish")
	}

	require.Equal(t, instance.Completed, inst.State())
	require.NoError(t, inst.LastErr())
}
