package instance_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
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
	"github.com/stretchr/testify/require"
)

func TestInstIvalidParams(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := instance.New(nil, nil, nil)
	require.Error(t, err)

	s, err := getSnapshot("invalid_params_test")
	require.NoError(t, err)

	_, err = instance.New(s, nil, nil)
	require.Error(t, err)

}

func TestInstance(t *testing.T) {
	s, err := getSnapshot("super simple process")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)
	inst, err := instance.New(s, nil, ep)

	st := inst.State()
	require.Equal(t, instance.Ready, st)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx, cancel))
}

// getSnapshot creates a simple process with user_name property
// StartEvent -> ServiceTask(print hello user_name) -> EndEvent
// and retruns its Snapshot.
func getSnapshot(pname string) (*snapshot.Snapshot, error) {
	p, err := process.New(pname,
		data.WithProperties(
			data.MustProperty(
				"UserName",
				data.MustItemDefinition(
					values.NewVariable("dr.Dobermann"),
					foundation.WithId("user_name")),
				data.ReadyDataState),
		))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	helloFunc, err := gooper.New(
		func(in *data.ItemDefinition) (*data.ItemDefinition, error) {
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

			userName, ok := in.Structure().Get().(string)
			if !ok {
				return nil,
					errs.New(
						errs.M("couldn't get string from operation input",
							errs.D("actual_type",
								reflect.TypeOf(in.Structure()).String())))
			}

			fmt.Println("\nHello, ", userName, "!")

			return nil, nil
		},
		errs.ObjectNotFound, errs.EmptyNotAllowed)
	if err != nil {
		return nil, err
	}

	op := service.MustOperation(
		"print user_name",
		common.MustMessage(
			"user_name",
			data.MustItemDefinition(
				values.NewVariable(""),
				foundation.WithId("user_name"))), nil, helloFunc)

	task, err := activities.NewServiceTask(
		"Print User Name", op, activities.WithoutParams())
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

	s, err := snapshot.New(p)
	if err != nil {
		return nil, err
	}

	return s, nil
}
