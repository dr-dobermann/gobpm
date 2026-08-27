package instance

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// countingDist records every announcement, so a test can tell registering a
// task from announcing one.
type countingDist struct {
	mu  sync.Mutex
	ids []string
}

func (c *countingDist) Distribute(
	_ context.Context, task interactor.TaskInfo,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ids = append(c.ids, task.TaskID)

	return nil
}

func (c *countingDist) Withdraw(context.Context, string) error { return nil }

func (c *countingDist) announced() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string{}, c.ids...)
}

// TestOnlyAFanOutOverHumanWorkIsAdopted: a track with no iteration seed has no
// per-instance identities to restore, and an iterated activity over anything
// else records none at all.
func TestOnlyAFanOutOverHumanWorkIsAdopted(t *testing.T) {
	ls, tr, _ := fanOutTrack(t)
	ls.inst.td = &countingDist{}

	ls.adoptRestoredTasks(context.Background(), []*track{tr})
	require.Empty(t, ls.tasks, "no seed, nothing to re-register")

	// a seeded track whose node is not a human task: an iterated Receive Task
	// parks on a subscription, which is addressed by a definition rather than
	// a task identity.
	recv, err := events.NewStartEvent("await", foundation.WithID("await"))
	require.NoError(t, err)

	other := &track{
		instance: ls.inst,
		steps:    []*stepInfo{{node: recv}},
		iterSeed: &checkpoint.IterationRecord{
			N: 1,
			Instances: []checkpoint.IterationInstance{
				{Ordinal: 0, State: instanceRunning, TaskID: "not-a-task"},
			},
		},
	}

	ls.adoptRestoredTasks(context.Background(), []*track{other})
	require.Empty(t, ls.tasks,
		"only a HUMAN task has a parked-work identity to re-register")
}

// TestRegisteringNothingIsNotATask: addTask and registerTask are reached for
// every born-parked waiter, and an empty id is how a non-human one says it has
// no task — it must not become a registry entry, nor an announcement.
func TestRegisteringNothingIsNotATask(t *testing.T) {
	ls, tr, _ := fanOutTrack(t)

	dist := &countingDist{}
	ls.inst.td = dist

	require.Nil(t, ls.registerTask(
		context.Background(), "", tr, tr.steps[0].node, 0))

	ls.addTask(context.Background(), "", tr, tr.steps[0].node, 0)

	require.Empty(t, ls.tasks)
	require.Empty(t, dist.announced())
}

// TestATrackWithNoCurrentStepIsSkipped: the adoption runs over every restored
// track at loop start, before anything has been validated for it — so a track
// that cannot say which node it stands on is passed over rather than taken as
// a fan-out over nothing.
func TestATrackWithNoCurrentStepIsSkipped(t *testing.T) {
	ls, _, _ := fanOutTrack(t)
	ls.inst.td = &countingDist{}

	stepless := &track{
		instance: ls.inst,
		steps:    []*stepInfo{nil},
		iterSeed: &checkpoint.IterationRecord{
			N: 1,
			Instances: []checkpoint.IterationInstance{
				{Ordinal: 0, State: instanceRunning, TaskID: "orphan"},
			},
		},
	}

	require.NotPanics(t, func() {
		ls.adoptRestoredTasks(context.Background(), []*track{stepless})
	})

	require.Empty(t, ls.tasks)
}
