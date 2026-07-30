package instance

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// SRD-074 M3 — the Ad-Hoc runtime seam: the Router answers succession at scope
// open and after each settle, an empty answer ends the container through the
// existing drain, and sequential ordering rejects a multi-successor answer.

// scriptedRouter answers from a queue of turns, recording the State it saw on
// each call — enough to assert both what ran and what the Router was told.
type scriptedRouter struct {
	mu    sync.Mutex
	turns [][]string
	seen  []adhoc.State
	err   error
}

func (r *scriptedRouter) Next(
	_ context.Context, s adhoc.State,
) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.seen = append(r.seen, s)

	if r.err != nil {
		return nil, r.err
	}

	if len(r.turns) == 0 {
		return nil, nil
	}

	next := r.turns[0]
	r.turns = r.turns[1:]

	return next, nil
}

func (r *scriptedRouter) states() []adhoc.State {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]adhoc.State(nil), r.seen...)
}

// recordOp builds an operation appending its name to log under mu.
func recordOp(
	t *testing.T, name string, mu *sync.Mutex, log *[]string,
) service.Operation {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			mu.Lock()
			defer mu.Unlock()

			*log = append(*log, name)

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// adHocInstance builds start -> triage(ad-hoc: a, b, c) -> end, each inner
// activity recording its run.
func adHocInstance(
	t *testing.T, r adhoc.Router, mu *sync.Mutex, log *[]string,
	opts ...options.Option,
) *Instance {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("adhoc")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	triage, err := activities.NewSubProcess("triage",
		append([]options.Option{activities.WithAdHoc(r)}, opts...)...)
	require.NoError(t, err)

	for _, name := range []string{"a", "b", "c"} {
		task, terr := activities.NewServiceTask(name,
			recordOp(t, name, mu, log), activities.WithoutParams())
		require.NoError(t, terr)
		require.NoError(t, triage.Add(task))
	}

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, triage, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, triage)
	require.NoError(t, err)
	_, err = flow.Link(triage, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	return inst
}

// adHocInstanceIDs builds the same shape but scripts the Router with the
// activity's generated ID instead of its name.
func adHocInstanceIDs(
	t *testing.T, r *scriptedRouter, mu *sync.Mutex, log *[]string,
) *Instance {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("adhoc-ids")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	triage, err := activities.NewSubProcess("triage", activities.WithAdHoc(r))
	require.NoError(t, err)

	task, err := activities.NewServiceTask("a",
		recordOp(t, "a", mu, log), activities.WithoutParams())
	require.NoError(t, err)
	require.NoError(t, triage.Add(task))

	r.turns = [][]string{{task.ID()}}

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, triage, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, triage)
	require.NoError(t, err)
	_, err = flow.Link(triage, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	return inst
}

func TestAdHocRouterDrivesSuccession(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	// Three turns, then the empty answer that ends the container.
	r := &scriptedRouter{turns: [][]string{{"b"}, {"a"}, {"b"}}}

	inst := adHocInstance(t, r, &mu, &log)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, []string{"b", "a", "b"}, log,
		"the Router's order is the run order, and an activity may repeat")
}

func TestAdHocEmptyFirstAnswerCompletesScope(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	r := &scriptedRouter{}

	inst := adHocInstance(t, r, &mu, &log)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Empty(t, log,
		"an empty first answer is a decision: the container completes having "+
			"run nothing")
}

func TestAdHocRouterSeesProgress(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	r := &scriptedRouter{turns: [][]string{{"a"}, {"a"}}}

	inst := adHocInstance(t, r, &mu, &log)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())

	states := r.states()
	require.Len(t, states, 3, "open, then one call per settle")

	require.Empty(t, states[0].Last, "no activity has settled at scope open")
	require.Empty(t, states[0].Completed)

	require.NotEmpty(t, states[1].Last, "the settling activity is named")
	require.Equal(t, 1, totalCounts(states[1].Completed),
		"one settled activity is counted by the second call")
	require.Equal(t, 2, totalCounts(states[2].Completed),
		"the counts accumulate across settles")
}

func TestAdHocSequentialRejectsMultipleSuccessors(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	r := &scriptedRouter{turns: [][]string{{"a", "b"}}}

	inst := adHocInstance(t, r, &mu, &log,
		activities.WithAdHocOrdering(activities.AdHocSequential))
	runToDone(t, inst)

	require.Equal(t, Terminated, inst.State(),
		"sequential ordering reports a multi-successor answer instead of "+
			"silently truncating it")
}

func TestAdHocUnknownActivityFails(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	r := &scriptedRouter{turns: [][]string{{"nowhere"}}}

	inst := adHocInstance(t, r, &mu, &log)
	runToDone(t, inst)

	require.Equal(t, Terminated, inst.State(),
		"an unresolvable activity is a loud failure, not a skipped step")
	require.Empty(t, log)
}

func TestAdHocRouterErrorFaultsInstance(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	r := &scriptedRouter{err: errs.New(errs.M("router is unavailable"))}

	inst := adHocInstance(t, r, &mu, &log)
	runToDone(t, inst)

	require.Equal(t, Terminated, inst.State(),
		"a failing Router faults the instance rather than ending the "+
			"container quietly")
}

func TestAdHocResolvesByID(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	// The Router names an activity by ID, the identifier a generated model
	// carries; the name fallback covers hand-built models.
	r := &scriptedRouter{}
	inst := adHocInstanceIDs(t, r, &mu, &log)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, []string{"a"}, log, "an id-named activity runs")
}

func TestAdHocWaitsForRemainingWhenAsked(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	// Two activities start together; the Router stops after the first settles,
	// so the second is awaited rather than cut short.
	r := &scriptedRouter{turns: [][]string{{"a", "b"}}}

	inst := adHocInstance(t, r, &mu, &log,
		activities.WithAdHocCancelRemaining(false))
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Len(t, log, 2,
		"a stopped container still lets its live activities finish")
}

func TestAdHocCancelsRemainingByDefault(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	// The metamodel's cancelRemainingInstances default is true, so a stop with
	// live activities cuts them short instead of waiting.
	r := &scriptedRouter{turns: [][]string{{"a", "b"}}}

	inst := adHocInstance(t, r, &mu, &log)
	runToDone(t, inst)

	require.Contains(t, []State{Completed, Terminated}, inst.State(),
		"the container settles rather than hanging on the canceled activity")
}

func TestAdHocOf(t *testing.T) {
	require.Nil(t, adHocOf(nil), "a nil node carries no ad-hoc spec")

	plain, err := activities.NewSubProcess("plain")
	require.NoError(t, err)
	require.Nil(t, adHocOf(plain),
		"a plain Sub-Process reports no ad-hoc spec, as a true nil")

	ah, err := activities.NewSubProcess("ah",
		activities.WithAdHoc(&scriptedRouter{}))
	require.NoError(t, err)
	require.NotNil(t, adHocOf(ah))
	require.Equal(t, activities.AdHocParallel, adHocOf(ah).Ordering())
}

func totalCounts(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}

	return total
}

func TestAdHocManualSelectionOffersAndActivates(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	// The Router offers two candidates each round; the host picks one, and the
	// container ends when the Router stops.
	r := &scriptedRouter{turns: [][]string{{"a", "b"}, {"c"}}}

	inst := adHocInstance(t, r, &mu, &log,
		activities.WithAdHocManualSelection())

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	go func() { _ = inst.Run(ctx) }()

	node := adHocNodeID(t, inst)

	// The container waits on the offer instead of running it.
	offered := waitForOffer(t, ctx, inst, node, 2)
	require.Len(t, offered, 2, "both candidates are offered, none run")

	mu.Lock()
	require.Empty(t, log, "nothing runs until the host chooses")
	mu.Unlock()

	require.NoError(t, inst.ActivateAdHoc(ctx, node, offered[0]))

	// After that activity settles the Router is asked again, so a fresh offer
	// replaces the consumed one.
	next := waitForOffer(t, ctx, inst, node, 1)
	require.Len(t, next, 1)
	require.NoError(t, inst.ActivateAdHoc(ctx, node, next[0]))

	require.Eventually(t, func() bool {
		return inst.State() == Completed
	}, 3*time.Second, 5*time.Millisecond,
		"the container completes when the Router stops")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, log, 2, "exactly the two activated activities ran")
}

func TestAdHocActivateRejectsUnofferedActivity(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	r := &scriptedRouter{turns: [][]string{{"a"}}}

	inst := adHocInstance(t, r, &mu, &log,
		activities.WithAdHocManualSelection())

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	go func() { _ = inst.Run(ctx) }()

	node := adHocNodeID(t, inst)
	waitForOffer(t, ctx, inst, node, 1)

	err := inst.ActivateAdHoc(ctx, node, "c")
	require.Error(t, err, "activating an unoffered activity is not a no-op")

	var ae *errs.ApplicationError
	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(errs.InvalidState))

	require.Error(t, inst.ActivateAdHoc(ctx, "no-such-node", "a"),
		"an unknown container is reported too")
	require.Error(t, inst.ActivateAdHoc(ctx, "", "a"),
		"both arguments are required")
}

// adHocNodeID finds the ad-hoc container's node id in the instance's graph.
func adHocNodeID(t *testing.T, inst *Instance) string {
	t.Helper()

	for _, n := range inst.s.Nodes {
		if adHocOf(n) != nil {
			return n.ID()
		}
	}

	t.Fatal("no ad-hoc container in the instance graph")

	return ""
}

// waitForOffer polls the container's view until it offers want candidates.
func waitForOffer(
	t *testing.T, ctx context.Context, inst *Instance, node string, want int,
) []string {
	t.Helper()

	var offered []string

	require.Eventually(t, func() bool {
		o, _, err := inst.AdHocView(ctx, node)
		if err != nil {
			return false
		}

		offered = o

		return len(o) == want
	}, 3*time.Second, 5*time.Millisecond, "the container never offered")

	return offered
}

func TestAdHocViewGuards(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	inst := adHocInstance(t, &scriptedRouter{}, &mu, &log)

	ctx := t.Context()

	_, _, err := inst.AdHocView(ctx, "")
	require.Error(t, err, "the container must be named")

	var ae *errs.ApplicationError

	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(errs.EmptyNotAllowed))

	// A finished instance answers rather than blocking: its loop is gone, so
	// the request can never be serviced.
	runToDone(t, inst)

	_, _, err = inst.AdHocView(ctx, "anything")
	require.Error(t, err, "a stopped instance reports instead of hanging")

	require.Error(t, inst.ActivateAdHoc(ctx, "anything", "a"),
		"an activation against a stopped instance reports too")
}

func TestAdHocViewCancelledContext(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	inst := adHocInstance(t, &scriptedRouter{}, &mu, &log)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// A caller that gave up gets its own context's error, not a hang: the
	// request is never posted.
	_, _, err := inst.AdHocView(ctx, "triage")
	require.ErrorIs(t, err, context.Canceled)

	require.ErrorIs(t, inst.ActivateAdHoc(ctx, "triage", "a"),
		context.Canceled)
}

// SRD-074 M5 — the KindAdHoc vocabulary: what was offered, what was activated
// and by whom, and why the container stopped, all reconstructible from the
// stream alone (FR-12, FR-13).

// factsOfKind returns, in emission order, every fact recorded for one kind —
// order is the assertion for the decision trail: an Activated must follow the
// Offered that named it.
func (r *obsRecorder) factsOfKind(
	kind observability.Kind,
) []observability.Fact {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := []observability.Fact{}

	for _, e := range r.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}

	return out
}

// byPhase groups facts by phase, keeping each group in emission order.
func byPhase(
	facts []observability.Fact,
) map[observability.Phase][]observability.Fact {
	out := map[observability.Phase][]observability.Fact{}
	for _, f := range facts {
		out[f.Phase] = append(out[f.Phase], f)
	}

	return out
}

func TestAdHocFacts(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	// Two activities, then the empty answer: three offers, two activations, one
	// terminal.
	r := &scriptedRouter{turns: [][]string{{"a"}, {"b"}}}

	inst := adHocInstance(t, r, &mu, &log)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)
	require.Equal(t, Completed, inst.State())

	groups := byPhase(rec.factsOfKind(observability.KindAdHoc))
	require.Len(t, groups[observability.PhaseOffered], 3,
		"one offer per Router answer, the empty one included")
	require.Len(t, groups[observability.PhaseActivated], 2)
	require.Len(t, groups[observability.PhaseCompleted], 1)

	container := adHocNodeID(t, inst)

	for _, f := range groups[observability.PhaseOffered] {
		require.Equal(t, container, f.NodeID,
			"an offer is the container's decision, not an activity's")
	}

	for i, f := range groups[observability.PhaseActivated] {
		require.Equal(t, []string{"a", "b"}[i], f.NodeName,
			"an activation names the activity that started")
		require.Equal(t, adHocByRouter,
			f.Details[observability.AttrSelectedBy],
			"an automatic container's Router is the selector")
	}

	terminal := groups[observability.PhaseCompleted][0]
	require.Equal(t, container, terminal.NodeID)
	require.Equal(t, adHocStopRouterEmpty,
		terminal.Details[observability.AttrStopReason])

	// The scope's own lifecycle keeps riding KindScope — the ad-hoc kind adds
	// only what is peculiar to routing, so nothing is reported twice (FR-12).
	scoped := rec.phasesOf(observability.KindScope)
	require.True(t, scoped[observability.PhaseOpened])
	require.True(t, scoped[observability.PhaseCompleted])
	require.False(t, scoped[observability.PhaseOffered],
		"a routing decision never rides the scope kind")
	require.False(t, scoped[observability.PhaseActivated])
}

func TestAdHocEmptyOfferEmitted(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	inst := adHocInstance(t, &scriptedRouter{}, &mu, &log)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)
	require.Equal(t, Completed, inst.State())

	facts := rec.factsOfKind(observability.KindAdHoc)
	require.Len(t, facts, 2, "the empty offer, then the terminal")

	require.Equal(t, observability.PhaseOffered, facts[0].Phase)

	candidates, present := facts[0].Details[observability.AttrCandidates]
	require.True(t, present,
		"the attribute is present-and-empty: a Router that considered the "+
			"state and stopped is not a Router that was never asked")
	require.Empty(t, candidates)

	require.Equal(t, observability.PhaseCompleted, facts[1].Phase)
	require.Equal(t, adHocStopRouterEmpty,
		facts[1].Details[observability.AttrStopReason])
}

func TestAdHocDecisionReconstructable(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	// A manual container over two rounds: the host is the selector, so the
	// stream must show who chose as well as what was on offer.
	r := &scriptedRouter{turns: [][]string{{"a", "b"}, {"c"}}}

	inst := adHocInstance(t, r, &mu, &log,
		activities.WithAdHocManualSelection())

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	go func() { _ = inst.Run(ctx) }()

	node := adHocNodeID(t, inst)

	first := waitForOffer(t, ctx, inst, node, 2)
	require.NoError(t, inst.ActivateAdHoc(ctx, node, first[0]))

	second := waitForOffer(t, ctx, inst, node, 1)
	require.NoError(t, inst.ActivateAdHoc(ctx, node, second[0]))

	require.Eventually(t, func() bool {
		return inst.State() == Completed
	}, 3*time.Second, 5*time.Millisecond)

	facts := rec.factsOfKind(observability.KindAdHoc)
	require.NotEmpty(t, facts)

	var (
		offered   []string
		activated int
	)

	for _, f := range facts {
		switch f.Phase {
		case observability.PhaseOffered:
			offered = strings.Split(
				f.Details[observability.AttrCandidates], ",")

		case observability.PhaseActivated:
			require.Contains(t, offered, f.NodeID,
				"every activation is preceded by an offer naming it — without "+
					"that pairing the case is unauditable")
			require.Equal(t, adHocByHost,
				f.Details[observability.AttrSelectedBy],
				"a manual container's host is the selector")

			activated++
		}
	}

	require.Equal(t, 2, activated)

	last := facts[len(facts)-1]
	require.Equal(t, observability.PhaseCompleted, last.Phase)
	require.Equal(t, adHocStopRouterEmpty,
		last.Details[observability.AttrStopReason],
		"the trail ends with the reason routing stopped")
}

func TestAdHocResolveRejectsHostWithoutNodes(t *testing.T) {
	// A container that holds no inner graph cannot answer a routing decision;
	// the failure names the host rather than silently routing nowhere.
	end, err := events.NewEndEvent("not-a-container")
	require.NoError(t, err)

	_, err = resolveAdHocNodes(end, []string{"a"})
	require.Error(t, err)

	var ae *errs.ApplicationError

	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(errs.InvalidState))
}
