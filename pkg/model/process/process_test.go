package process_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

func TestProcess(t *testing.T) {
	var roles []*hi.ResourceRole
	for _, rn := range []string{
		"process_owner",
		"task1_executor", "task2_executor",
	} {
		roles = append(roles,
			hi.MustResourceRole(
				rn, nil, nil, nil))
	}

	rs, err := data.NewDataState("ready")
	require.NoError(t, err)
	require.NotEmpty(t, rs)

	x := values.NewVariable(42)
	xProp := data.MustProperty("x", data.MustItemDefinition(x), rs)

	t.Run("new with invalid parameters and options",
		func(t *testing.T) {
			p, err := process.New("")
			require.Error(t, err)
			require.Empty(t, p)

			// nil roles and properties
			_, err = process.New("invalid roles & props",
				activities.WithRoles(nil,
					roles[0]),
				data.WithProperties(
					data.MustProperty("x", data.MustItemDefinition(x), rs),
					nil))
			require.NoError(t, err)

			// invalid option
			_, err = process.New("invalid options",
				activities.WithCompensation())
			require.Error(t, err)
		})

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	require.NoError(t, data.CreateDefaultStates())

	task1, err := activities.NewServiceTask(
		"task1",
		service.MustOperation(
			"runs_service1",
			nil, nil, nil),
		activities.WithEmptySet(
			"default input set for task1",
			"",
			data.Input),
		activities.WithSet(
			"output set for task1",
			"",
			data.Output,
			data.DefaultSet,
			[]*data.Parameter{
				data.MustParameter(
					"y",
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(23.02),
							foundation.WithId("y")),
						data.ReadyDataState)),
			}))
	require.NoError(t, err)

	task2, err := activities.NewServiceTask(
		"task2",
		service.MustOperation(
			"runs_service2",
			nil, nil, nil),
		activities.WithSet(
			"input set for task2",
			"",
			data.Input,
			data.DefaultSet,
			[]*data.Parameter{
				data.MustParameter(
					"y",
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(0.0),
							foundation.WithId("y")),
						nil)),
			}),
		activities.WithEmptySet(
			"default output set for task2", "", data.Output))
	require.NoError(t, err)

	end, err := events.NewEndEvent("finish")
	require.NoError(t, err)

	extra, err := activities.NewServiceTask("external task",
		service.MustOperation("fiction", nil, nil, nil),
		activities.WithoutParams())
	require.NoError(t, err)

	f1, err := flow.Link(start, task1)
	require.NoError(t, err)

	f2, err := flow.Link(task1, task2, options.WithName("print results"))
	require.NoError(t, err)

	f3, err := flow.Link(task2, end)
	require.NoError(t, err)

	do, err := dataobjects.New(
		"X",
		data.MustItemDefinition(
			values.NewVariable(100),
			foundation.WithId("y")),
		data.ReadyDataState)
	require.NoError(t, err)

	t.Run("new process",
		func(t *testing.T) {
			p, err := process.New("simple process",
				activities.WithRoles(roles[0]),
				data.WithProperties(xProp),
				foundation.WithId("simple process"))
			require.NoError(t, err)
			require.NotEmpty(t, p)

			require.Equal(t, "simple process", p.Name())
			props := p.Properties()
			require.Len(t, props, 1)
			require.Equal(t, "x", props[0].Name())

			for _, n := range []flow.Node{start, task1, task2, end} {
				require.NoError(t, p.Add(n))
			}

			// add empty node
			require.Error(t, p.Add(nil))

			// add duplicate node
			require.Error(t, p.Add(start))

			for _, f := range []*flow.SequenceFlow{f1, f2, f3} {
				require.NoError(t, p.Add(f))
			}

			// add duplicate flow
			require.Error(t, p.Add(f1))

			nn := p.Nodes()
			require.Equal(t, 4, len(nn))
			for _, n := range nn {
				t.Log(n.Name())
			}

			// add data object between task1 and task2
			err = do.AssociateSource(
				task1,
				[]string{"y"},
				nil)
			require.NoError(t, err)

			err = do.AssociateTarget(task2, nil)
			require.NoError(t, err)

			// ask invalid nodes type
			require.Empty(t, p.Nodes(flow.NodeType("invalid node type")))

			_, err = flow.Link(start, task2)
			require.NoError(t, err)

			ff := p.Flows()
			require.Equal(t, 4, len(ff))
			for _, f := range ff {
				require.NoError(t, f.Validate())

				t.Log(f.Id(), " ", f.Name())
			}

			_, err = flow.Link(extra, task2)
			require.Error(t, err)
			t.Log(err)
		})
}
