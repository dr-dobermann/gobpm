package process_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

func TestProcess(t *testing.T) {
	var roles []*activities.ResourceRole
	for _, rn := range []string{"process_owner",
		"task1_executor", "task2_executor"} {
		roles = append(roles,
			activities.MustResourceRole(
				rn, nil, nil, nil))
	}

	rs, err := data.NewDataState("ready")
	require.NoError(t, err)
	require.NotEmpty(t, rs)

	x := values.NewVariable(42)
	xProp := data.MustProperty("x", data.MustItemDefinition(x), rs)

	t.Run("new with invalid parameters and options",
		func(t *testing.T) {
			p, err := process.NewProcess("")
			require.Error(t, err)
			require.Empty(t, p)

			// nil roles and properties
			_, err = process.NewProcess("invalid roles & props",
				activities.WithRoles(nil,
					roles[0]),
				data.WithProperties(
					data.MustProperty("x", data.MustItemDefinition(x), rs),
					nil))
			require.NoError(t, err)

			// invalid option
			_, err = process.NewProcess("invalid options",
				activities.WithCompensation())
			require.Error(t, err)
		})

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	task1, err := activities.NewServiceTask(
		"task1",
		service.MustOperation(
			"runs_service1",
			nil, nil, nil),
		activities.WithoutParams())
	require.NoError(t, err)

	task2, err := activities.NewServiceTask(
		"task2",
		service.MustOperation(
			"runs_service2",
			nil, nil, nil),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("finish")
	require.NoError(t, err)

	extra, err := activities.NewServiceTask("external task",
		service.MustOperation("fiction", nil, nil, nil),
		activities.WithoutParams())

	f1, err := start.Link(task1)
	require.NoError(t, err)

	f2, err := task1.Link(task2, options.WithName("print results"))
	require.NoError(t, err)

	f3, err := task2.Link(end)
	require.NoError(t, err)

	t.Run("new process",
		func(t *testing.T) {

			p, err := process.NewProcess("simple process",
				activities.WithRoles(roles[0]),
				data.WithProperties(xProp))
			require.NoError(t, err)
			require.NotEmpty(t, p)

			for _, n := range []flow.FlowNode{start, task1, task2, end} {
				require.NoError(t, p.AddNode(n))
			}

			for _, f := range []*flow.SequenceFlow{f1, f2, f3} {
				require.NoError(t, p.AddFlow(f))
			}

			nn := p.Nodes()
			require.Equal(t, 4, len(nn))
			for _, n := range nn {
				t.Log(n.GetNode().Name())
			}

			ff := p.Flows()
			require.Equal(t, 3, len(ff))
			for _, f := range ff {
				require.NoError(t, f.Validate())

				t.Log(f.Id(), " ", f.Name())
			}

			_, err = extra.Link(task1)
			require.Error(t, err)
			t.Log(err)
		})
}
