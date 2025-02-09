package instance_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/monitor/logmon"
	"github.com/stretchr/testify/require"
)

func TestInstIvalidParams(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := instance.New(nil, nil, nil, nil, nil)
	require.Error(t, err)

	s, err := getSnapshot("invalid_params_test")
	require.NoError(t, err)

	_, err = instance.New(s, nil, nil, nil, nil)
	require.Error(t, err)
}

// func TestInstance(t *testing.T) {
// 	s, err := getSnapshot("super simple process")
// 	require.NoError(t, err)
//
// 	ep := mockeventproc.NewMockEventProducer(t)
// 	inst, err := instance.New(s, nil, ep, nil)
// 	require.NoError(t, err)
//
// 	st := inst.State()
// 	require.Equal(t, instance.Ready, st)
//
// 	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
// 	defer cancel()
//
// 	require.NoError(t, inst.Run(ctx))
// }

func TestMonitoring(t *testing.T) {
	s, err := getSnapshot("monitoring")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	logger := slog.New(
		slog.NewTextHandler(
			os.Stdout,
			&slog.HandlerOptions{
				Level: slog.LevelDebug,
			}))
	m, err := logmon.New(logger)
	require.NoError(t, err)

	inst, err := instance.New(s, nil, ep, nil, m)
	require.NoError(t, err)

	// test runtime variables
	rvs, err := scope.NewDataPath("/monitoring/RUNTIME")
	require.NoError(t, err)

	_, err = inst.GetData(rvs, "INVALID_NAME")
	require.Error(t, err)

	ctx := context.Background()

	tc, err := inst.GetData(rvs, instance.TracksCount)
	require.NoError(t, err)
	require.Equal(t, 1, tc.Value().Get(ctx).(int))

	st, err := inst.GetData(rvs, instance.CurrState)
	require.NoError(t, err)
	require.Equal(t, instance.Ready, st.Value().Get(ctx).(instance.State))

	start, err := inst.GetData(rvs, instance.StartedAt)
	require.NoError(t, err)
	require.True(t, start.Value().Get(ctx).(time.Time).IsZero())

	ctx, cancel := context.WithCancel(context.Background())

	// test instance run
	err = inst.Run(ctx)
	require.NoError(t, err)

	log.Println("instance runned")

	time.Sleep(3 * time.Second)

	cancel()
}

// getSnapshot creates a simple process with user_name property
// StartEvent -> ServiceTask(print hello user_name) -> EndEvent
// and retruns its Snapshot.
func getSnapshot(pname string) (*snapshot.Snapshot, error) {
	p, err := process.New(pname,
		data.WithProperties(
			data.MustProperty(
				"user_name",
				data.MustItemDefinition(
					values.NewVariable("Dr. Dobermann"),
					foundation.WithId("user_name")),
				data.ReadyDataState)))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	helloFunc, err := gooper.New(
		func(ctx context.Context, in *data.ItemDefinition) (*data.ItemDefinition, error) {
			const inId = "user_name"

			if in == nil {
				return nil,
					errs.New(
						errs.M("empty operation input"),
						errs.C(errs.EmptyNotAllowed))
			}

			if in.Id() != inId {
				return nil,
					errs.New(
						errs.M("not expected operation parameter"),
						errs.C(errs.ObjectNotFound),
						errs.D("expected_id", inId),
						errs.D("got_id", in.Id()))
			}

			userName, ok := in.Structure().Get(context.Background()).(string)
			if !ok {
				return nil,
					errs.New(
						errs.M("couldn't get user name as operation input",
							errs.D("actual_type",
								reflect.TypeOf(in.Structure()).String())))
			}

			fmt.Println("\nHello, ", userName, "!")

			return nil, nil
		},
		errs.ObjectNotFound,
		errs.EmptyNotAllowed)
	if err != nil {
		return nil, err
	}

	op := service.MustOperation(
		"print user_name",
		common.MustMessage(
			"user_name",
			data.MustItemDefinition(
				values.NewVariable(""),
				foundation.WithId("user_name"))),
		nil,
		helloFunc)

	task, err := activities.NewServiceTask(
		"Print User Name", op, activities.WithoutParams())
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, err
	}

	// register nodes
	for _, fe := range []flow.Element{start, task, end} {
		if err := p.Add(fe); err != nil {
			return nil, err
		}
	}

	// link nodes between each others
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

	s, err := snapshot.New(p)
	if err != nil {
		return nil, err
	}

	return s, nil
}
