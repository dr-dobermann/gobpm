package instance

// SRD-079 M1 — the incident entity and the raise path (ADR-036). A technical
// failure ends its track in TrackIncident and opens a durable incident on the
// instance instead of faulting it (T-1); an uncaught BpmnError does the same
// (T-2); an invariant violation keeps the fatal path (T-3); a worker's terminal
// fault — the dispatcher's retries-exhausted delivery — raises the incident at
// the service task (T-4). The raise emits the KindFault/PhaseIncident fact
// (T-14's first slice).

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// failingOp builds a ServiceTask operation failing with the given error.
func failingOp(t *testing.T, err error) service.Operation {
	t.Helper()

	op, opErr := gooper.New("failing-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			return nil, err
		})
	require.NoError(t, opErr)

	return op
}

// okOp builds a ServiceTask operation that succeeds with no output.
func okOp(t *testing.T) service.Operation {
	t.Helper()

	op, err := gooper.New("ok-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// factRecorder collects the instance's observation stream.
type factRecorder struct {
	m     sync.Mutex
	facts []observability.Fact
}

func (fr *factRecorder) add(f observability.Fact) {
	fr.m.Lock()
	defer fr.m.Unlock()

	fr.facts = append(fr.facts, f)
}

// byPhase returns the recorded facts with the given phase.
func (fr *factRecorder) byPhase(p observability.Phase) []observability.Fact {
	fr.m.Lock()
	defer fr.m.Unlock()

	out := []observability.Fact{}

	for _, f := range fr.facts {
		if f.Phase == p {
			out = append(out, f)
		}
	}

	return out
}

// forkedFailProcess builds start → split(parallel) → {fail(op) → endA,
// ok → endB}: the branch that fails and the sibling that must complete.
func forkedFailProcess(
	t *testing.T,
	op service.Operation,
) (*process.Process, string) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("srd079-m1")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	split, err := gateways.NewParallelGateway()
	require.NoError(t, err)

	failTask, err := activities.NewServiceTask("fail-task", op,
		activities.WithoutParams())
	require.NoError(t, err)

	okTask, err := activities.NewServiceTask("ok-task", okOp(t),
		activities.WithoutParams())
	require.NoError(t, err)

	endA, err := events.NewEndEvent("end-a")
	require.NoError(t, err)

	endB, err := events.NewEndEvent("end-b")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, failTask, okTask, endA, endB} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, split)
	require.NoError(t, err)
	_, err = flow.Link(split, failTask)
	require.NoError(t, err)
	_, err = flow.Link(split, okTask)
	require.NoError(t, err)
	_, err = flow.Link(failTask, endA)
	require.NoError(t, err)
	_, err = flow.Link(okTask, endB)
	require.NoError(t, err)

	return p, failTask.ID()
}

// forkedFailInstance is forkedFailProcess run into an instance with a fact
// recorder attached.
func forkedFailInstance(
	t *testing.T,
	op service.Operation,
) (*Instance, *factRecorder, string) {
	t.Helper()

	p, failID := forkedFailProcess(t, op)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	fr := &factRecorder{}
	inst.AddObserver(fr.add)

	return inst, fr, failID
}

// runToLoopExit runs the instance and waits for its loop to return — an
// incident park exits the loop without settling, so Done() is the right gate.
func runToLoopExit(t *testing.T, inst *Instance) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("instance loop did not exit")
	}
}

// requireOpenIncidentAt asserts exactly one open incident, at the given node,
// and returns it.
func requireOpenIncidentAt(
	t *testing.T,
	inst *Instance,
	nodeID string,
) *incident {
	t.Helper()

	require.Equal(t, 1, inst.openIncidents())

	for _, inc := range inst.incidents {
		if inc.state.open() {
			require.Equal(t, nodeID, inc.nodeID)

			return inc
		}
	}

	t.Fatal("no open incident found")

	return nil
}

// TestIncidentStateNames pins the state→name table (the kind-name sync
// convention) and the bounds guard.
func TestIncidentStateNames(t *testing.T) {
	for st, want := range map[incidentState]string{
		incidentOpen:           "open",
		incidentRetryScheduled: "retry-scheduled",
		incidentResolved:       "resolved",
		incidentDeadLettered:   "dead-lettered",
		incidentOvertaken:      "overtaken",
	} {
		require.Equal(t, want, st.String())
	}

	require.Equal(t, "incidentState(99)", incidentState(99).String())
}

// TestOpenIncidentAtMatch covers the re-arm lookup: the open incident for the
// same node+scope is found; a closed one, a different node, or a different
// scope is not.
func TestOpenIncidentAtMatch(t *testing.T) {
	inst := &Instance{incidents: map[string]*incident{
		"closed": {nodeID: "n1", scopePath: "/p", state: incidentResolved},
		"open":   {nodeID: "n1", scopePath: "/p", state: incidentOpen},
	}}

	require.Equal(t, inst.incidents["open"],
		inst.openIncidentAt("n1", "/p", ""))
	require.Nil(t, inst.openIncidentAt("n2", "/p", ""))
	require.Nil(t, inst.openIncidentAt("n1", "/other", ""))
}

// TestIncidentInternalEdges covers the small lock-free branches: no snapshot
// before any incident, the open-state filter, and the TokenIncident vocabulary
// rows.
func TestIncidentInternalEdges(t *testing.T) {
	inst := &Instance{incidents: map[string]*incident{}}
	require.Nil(t, inst.IncidentViews())

	inst.incidents["c"] = &incident{state: incidentResolved}
	inst.incidents["o"] = &incident{state: incidentRetryScheduled}
	require.Equal(t, 1, inst.openIncidents())

	require.Equal(t, "Incident", TokenIncident.String())
	require.NoError(t, TokenIncident.Validate())
}

// TestIncidentSnapshotFailurePath: a snapshot the plane cannot serve (a path
// outside it) logs and yields nil — it never masks the incident.
func TestIncidentSnapshotFailurePath(t *testing.T) {
	inst, _, _ := forkedFailInstance(t, failingOp(t, fmt.Errorf("unused")))
	ls := newLoopState(inst)

	res := ls.snapshotIncidentScope(context.Background(),
		&track{scopePath: "/elsewhere"})
	require.Nil(t, res)
}

// T-1: an untyped technical failure raises an incident — the failing track ends
// TrackIncident, the sibling branch completes, and the instance stays alive.
func TestTechnicalFailureRaisesIncident(t *testing.T) {
	inst, _, failID := forkedFailInstance(t, failingOp(t, fmt.Errorf("boom")))

	runToLoopExit(t, inst)

	require.Equal(t, Active, inst.State(),
		"an incident must not fault the instance")
	require.Nil(t, inst.LastErr())

	inc := requireOpenIncidentAt(t, inst, failID)
	require.Equal(t, 1, inc.attempts)
	require.Contains(t, inc.cause, "boom")

	ft := inst.tracks[inc.trackID]
	require.NotNil(t, ft)
	require.True(t, ft.inState(TrackIncident))

	// the sibling branch ran to its end: some track completed normally.
	ended := 0

	for _, tr := range inst.tracks {
		if tr.inState(TrackEnded) {
			ended++
		}
	}
	require.NotZero(t, ended, "the sibling branch must complete")
}

// T-2: an uncaught BpmnError raises an incident at the throwing node; the
// Uncaught fact is preserved on the stream.
func TestUncaughtBpmnErrorRaisesIncident(t *testing.T) {
	inst, fr, failID := forkedFailInstance(t,
		failingOp(t, &events.BpmnError{Code: "E-UNCAUGHT"}))

	runToLoopExit(t, inst)

	require.Equal(t, Active, inst.State())
	requireOpenIncidentAt(t, inst, failID)

	require.NotEmpty(t, fr.byPhase(observability.PhaseUncaught),
		"the Uncaught fact must survive the incident path")
}

// T-3: an invariant violation denies the engine's own state and keeps the
// fatal path — no incident, the instance faults.
func TestInvariantViolationFaultsInstance(t *testing.T) {
	inst, _, _ := forkedFailInstance(t,
		failingOp(t, errs.Invariant("engine state denied")))

	runToLoopExit(t, inst)

	require.Equal(t, Terminated, inst.State())
	require.Error(t, inst.LastErr())
	require.Zero(t, inst.openIncidents())
}

// T-4: a worker's terminal fault — what the dispatcher delivers when its
// retries exhaust (ADR-021 §2.7) — raises the incident at the service task.
func TestJobExhaustionRaisesIncident(t *testing.T) {
	disp := &capDispatcher{}
	inst, cancel := serviceTaskWorkerInst(t, disp,
		service.MustOperation("op", nil, nil, nil))
	defer cancel()

	job := waitForJob(t, disp)

	require.NoError(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerFault(job.ID,
			tasks.Fault{Cause: fmt.Errorf("infra down")})))

	require.Eventually(t, func() bool { return inst.OpenIncidents() == 1 },
		2*time.Second, 5*time.Millisecond)
	require.Equal(t, Active, inst.State(),
		"job exhaustion opens an incident, not an instance fault")
}

// T-5: while the incident is open, the token stays visible at the failing
// node in the TokenIncident state — preserved, not consumed (FR-4) — and the
// incident view carries the record.
func TestIncidentTokenVisible(t *testing.T) {
	inst, _, failID := forkedFailInstance(t, failingOp(t, fmt.Errorf("boom")))

	runToLoopExit(t, inst)

	var found bool

	for _, tok := range inst.GetTokens() {
		if tok.Node.ID() == failID {
			require.Equal(t, TokenIncident, tok.State)

			found = true
		}
	}

	require.True(t, found, "the incident token must stay visible at the node")

	views := inst.IncidentViews()
	require.Len(t, views, 1)
	require.Equal(t, failID, views[0].NodeID)
	require.Equal(t, "open", views[0].State)
	require.Equal(t, 1, views[0].Attempts)
}

// T-5a: the incident carries a failure-time snapshot of the variables visible
// from the failing node's scope — the process property is in it — and the
// incident pins its scope open (the sub-process scope survives the failure
// that would otherwise drain it).
func TestIncidentDataSnapshot(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("srd079-snap",
		data.WithProperties(
			data.MustProperty("budget",
				data.MustItemDefinition(values.NewVariable(42),
					foundation.WithID("budget")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)

	fail, err := activities.NewServiceTask("fail-task",
		failingOp(t, fmt.Errorf("boom")), activities.WithoutParams())
	require.NoError(t, err)

	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, fail, sEnd} {
		require.NoError(t, sp.Add(e))
	}

	_, err = flow.Link(sStart, fail)
	require.NoError(t, err)
	_, err = flow.Link(fail, sEnd)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, sp, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, sp)
	require.NoError(t, err)
	_, err = flow.Link(sp, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	// The loop does NOT exit here: the SP host legitimately keeps waiting on
	// its pinned scope, so the instance stays live with the open incident —
	// observe through the lock-free surfaces only.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool { return inst.OpenIncidents() == 1 },
		3*time.Second, 5*time.Millisecond)

	views := inst.IncidentViews()
	require.Len(t, views, 1)
	require.Equal(t, fail.ID(), views[0].NodeID)
	require.NotEmpty(t, views[0].Data, "the failure-time snapshot must exist")
	require.Contains(t, string(views[0].Data), "budget",
		"the visible process property is in the snapshot")

	// scope pinning: the SP scope is still open beside the root — without
	// the pin, the incident track's exit would have drained it.
	require.Len(t, inst.sc.plane.OpenPaths(), 2,
		"the incident's scope must stay open while the incident is")
}

// T-6: raising an incident is a persist point — the checkpoint carries the
// schema-3 incident table under StatusActiveIncidents — and a restore rebuilds
// the incident, its snapshot, its count and its token view.
func TestIncidentCheckpointRecovery(t *testing.T) {
	p, failID := forkedFailProcess(t, failingOp(t, fmt.Errorf("boom")))

	s, err := snapshot.New(p)
	require.NoError(t, err)

	rt := enginert.Default()

	inst, err := New(s, scope.EmptyDataPath, rt, &recordingProducer{}, nil,
		WithCheckpointing("engine-A", time.Minute))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	// the raise checkpoints on its own transition: poll the repository for
	// the record carrying the incident.
	var doc *checkpoint.Document

	require.Eventually(t, func() bool {
		rec, ok, lerr := rt.Repository().Load(ctx, inst.ID())
		if lerr != nil || !ok ||
			rec.Status != repository.StatusActiveIncidents {
			return false
		}

		d, uerr := checkpoint.Unmarshal(rec.Payload)
		if uerr != nil || len(d.Incidents) == 0 {
			return false
		}

		doc = d

		return true
	}, 3*time.Second, 5*time.Millisecond)

	require.Equal(t, 3, doc.Schema)
	require.Len(t, doc.Incidents, 1)
	require.Equal(t, failID, doc.Incidents[0].NodeID)
	require.Equal(t, "open", doc.Incidents[0].State)
	require.Equal(t, 1, doc.Incidents[0].Attempts)
	require.Contains(t, doc.Incidents[0].Cause, "boom")
	require.NotEmpty(t, doc.Incidents[0].Data,
		"the failure-time snapshot persists")

	cancel() // the source instance is done serving

	// restore into a fresh snapshot pinned to the recorded version.
	s2, err := snapshot.New(p)
	require.NoError(t, err)
	s2.Version = doc.Version

	restored, err := Restore(doc, s2, scope.EmptyDataPath,
		enginert.Default(), &recordingProducer{}, nil, nil)
	require.NoError(t, err)

	require.Equal(t, 1, restored.OpenIncidents(),
		"the incident survives the restart")

	views := restored.IncidentViews()
	require.Len(t, views, 1)
	require.Equal(t, failID, views[0].NodeID)
	require.Contains(t, views[0].Cause, "boom")
	require.NotEmpty(t, views[0].Data)

	var found bool

	for _, tok := range restored.GetTokens() {
		if tok.Node.ID() == failID {
			require.Equal(t, TokenIncident, tok.State)

			found = true
		}
	}

	require.True(t, found,
		"the incident token projects from the restored record")
}

// TestIncidentRecordRoundTrip pins the schema-3 codec: an incident record
// survives Marshal → Unmarshal bit-exact, and a schema-2 document (no
// incidents) still reads.
func TestIncidentRecordRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)

	doc := &checkpoint.Document{
		InstanceID: "i1",
		ProcessID:  "p1",
		Incidents: []checkpoint.IncidentRecord{{
			FirstAt:    at,
			LastAt:     at.Add(time.Minute),
			ID:         "inc1",
			NodeID:     "n1",
			TrackID:    "t1",
			NodeName:   "task",
			ScopePath:  "/p1",
			ScopeSeg:   "seg",
			Cause:      "boom",
			CauseClass: "OPERATION_FAILED",
			State:      "dead-lettered",
			Prev:       []string{"t0"},
			Data:       []byte(`[{"name":"x"}]`),
			Attempts:   3,
		}},
	}

	raw, err := doc.Marshal()
	require.NoError(t, err)

	back, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, 3, back.Schema)
	require.Equal(t, doc.Incidents, back.Incidents)

	// a pre-incident (schema 2) document still reads.
	old := &checkpoint.Document{InstanceID: "i2", ProcessID: "p2"}
	oldRaw, err := old.Marshal()
	require.NoError(t, err)

	var loose map[string]any
	require.NoError(t, json.Unmarshal(oldRaw, &loose))
	loose["schema"] = 2
	oldRaw, err = json.Marshal(loose)
	require.NoError(t, err)

	back2, err := checkpoint.Unmarshal(oldRaw)
	require.NoError(t, err)
	require.Empty(t, back2.Incidents)
}

// T-14 (raise slice): the raise emits KindFault/PhaseIncident with the action
// and the cause.
func TestIncidentRaisedFact(t *testing.T) {
	inst, fr, failID := forkedFailInstance(t, failingOp(t, fmt.Errorf("boom")))

	runToLoopExit(t, inst)

	// PhaseIncident appears twice: the node-progress fact from the track's
	// state flip (trackPhase) and the KindFault raise fact — assert the fault.
	var raised []observability.Fact

	for _, f := range fr.byPhase(observability.PhaseIncident) {
		if f.Kind == observability.KindFault {
			raised = append(raised, f)
		}
	}

	require.Len(t, raised, 1)
	require.Equal(t, failID, raised[0].NodeID)
	require.Equal(t, "raised", raised[0].Details["action"])
	require.Contains(t, raised[0].Details[observability.AttrError], "boom")
}
