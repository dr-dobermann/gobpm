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
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
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

	rs, err := data.NewSrcState("ready")
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
		activities.WithParameters(data.Output,
			data.MustParameter(
				"y",
				data.MustItemAwareElement(
					data.MustItemDefinition(
						values.NewVariable(23.02),
						foundation.WithID("y")),
					data.ReadyDataState))))
	require.NoError(t, err)

	task2, err := activities.NewServiceTask(
		"task2",
		service.MustOperation(
			"runs_service2",
			nil, nil, nil),
		activities.WithParameters(data.Input,
			data.MustParameter(
				"y",
				data.MustItemAwareElement(
					data.MustItemDefinition(
						values.NewVariable(0.0),
						foundation.WithID("y")),
					nil))))
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
			foundation.WithID("y")),
		data.ReadyDataState)
	require.NoError(t, err)

	t.Run("new process",
		func(t *testing.T) {
			p, err := process.New("simple process",
				activities.WithRoles(roles[0]),
				data.WithProperties(xProp),
				foundation.WithID("simple process"))
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

				t.Log(f.ID(), " ", f.Name())
			}

			_, err = flow.Link(extra, task2)
			require.Error(t, err)
			t.Log(err)
		})
}

// fakeElement is a flow.Element whose EType lies about its concrete type: it
// reports NodeElement or SequenceBaseElement while being neither a flow.Node
// nor a *flow.SequenceFlow. It exercises the comma-ok guards in Process.Add.
type fakeElement struct {
	id    string
	etype flow.ElementType
}

func (f fakeElement) ID() string                        { return f.id }
func (f fakeElement) Docs() []*foundation.Documentation { return nil }
func (f fakeElement) Name() string                      { return "fake" }
func (f fakeElement) Container() flow.Container         { return nil }
func (f fakeElement) EType() flow.ElementType           { return f.etype }
func (f fakeElement) BindTo(flow.Container) error       { return nil }
func (f fakeElement) Unbind() error                     { return nil }

func TestProcessAddTypeMismatch(t *testing.T) {
	p, err := process.New("type mismatch")
	require.NoError(t, err)

	// reports NodeElement but is not a flow.Node
	require.Error(t,
		p.Add(fakeElement{id: "fake_node", etype: flow.NodeElement}))

	// reports SequenceBaseElement but is not a *flow.SequenceFlow
	require.Error(t,
		p.Add(fakeElement{id: "fake_flow", etype: flow.SequenceBaseElement}))
}

func TestProcessValidate(t *testing.T) {
	t.Run("well-formed graph passes",
		func(t *testing.T) {
			p, err := process.New("well-formed")
			require.NoError(t, err)

			start, err := events.NewStartEvent("start")
			require.NoError(t, err)
			end, err := events.NewEndEvent("end")
			require.NoError(t, err)

			require.NoError(t, p.Add(start))
			require.NoError(t, p.Add(end))

			// start is in p, so the flow auto-adds into p.
			_, err = flow.Link(start, end)
			require.NoError(t, err)

			require.NoError(t, p.Validate())
		})

	// Note: a flow with exactly one endpoint outside the process is not
	// constructible — flow.SequenceFlow.BindTo requires the source and target
	// to share a container, so a flow added to the process has both endpoints
	// in it or (as below) both outside it.
	t.Run("flow whose endpoints are not in the process fails",
		func(t *testing.T) {
			p, err := process.New("dangling endpoints")
			require.NoError(t, err)

			start, err := events.NewStartEvent("start")
			require.NoError(t, err)
			end, err := events.NewEndEvent("end")
			require.NoError(t, err)

			// neither node is added: both have a nil container, so the flow
			// can be added to the process (container-consistent), and Validate
			// must flag both the missing source and the missing target.
			f, err := flow.Link(start, end)
			require.NoError(t, err)
			require.NoError(t, p.Add(f))

			require.Error(t, p.Validate())
		})
}

// TestProcessValidateComplexGateway covers the per-node Validate() hook in
// Process.Validate (SRD-023 M2): a ComplexGateway whose activation threshold exceeds
// its incoming-flow count is rejected at registration, a valid one passes, and nodes
// without a Validate() method are untouched.
func TestProcessValidateComplexGateway(t *testing.T) {
	newProc := func(t *testing.T, threshold int) *process.Process {
		t.Helper()

		p, err := process.New("complex-validate")
		require.NoError(t, err)

		cg, err := gateways.NewComplexGateway(
			gateways.WithActivationThreshold(threshold),
			gateways.WithDirection(gateways.Converging))
		require.NoError(t, err)

		s1, err := events.NewStartEvent("s1")
		require.NoError(t, err)
		s2, err := events.NewStartEvent("s2")
		require.NoError(t, err)
		end, err := events.NewEndEvent("end")
		require.NoError(t, err)

		for _, n := range []flow.Element{cg, s1, s2, end} {
			require.NoError(t, p.Add(n))
		}

		_, err = flow.Link(s1, cg)
		require.NoError(t, err)
		_, err = flow.Link(s2, cg)
		require.NoError(t, err)
		_, err = flow.Link(cg, end)
		require.NoError(t, err)

		return p
	}

	// threshold 2 == incoming 2 → valid (and the start/end events, which have no
	// Validate(), don't interfere).
	require.NoError(t, newProc(t, 2).Validate())

	// threshold 5 > incoming 2 → rejected by the per-node hook.
	require.Error(t, newProc(t, 5).Validate())
}
