package process_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
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

// TestProcessElementsNodesRemove covers Elements(), Remove() (nil, not-found,
// node and flow removal) and the has() helper reached by Nodes() with a valid
// type filter.
func TestProcessElementsNodesRemove(t *testing.T) {
	p, err := process.New("ops")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	// link before binding the nodes, so the flow is not auto-added to the
	// process; add the nodes and the flow explicitly afterwards.
	f, err := flow.Link(start, end)
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	require.NoError(t, p.Add(f))

	// Nodes() with a valid type filter drives the has() helper.
	require.Len(t, p.Nodes(flow.EventNodeType), 2)
	require.Empty(t, p.Nodes(flow.GatewayNodeType))

	// Elements() returns every node and flow.
	require.Len(t, p.Elements(), 3)

	// Remove(): a nil element and an element not in the process are rejected.
	require.Error(t, p.Remove(nil))

	other, err := events.NewStartEvent("other")
	require.NoError(t, err)
	require.Error(t, p.Remove(other))

	// removing the flow and a node succeeds and unbinds them.
	require.NoError(t, p.Remove(f))
	require.NoError(t, p.Remove(start))
	require.Len(t, p.Elements(), 1)
}

// TestStartConditionalRejected — the SRD-048 top-level placement gate
// (ADR-006 v.3 §2.7): a Conditional trigger on a Start Event of a
// top-level Process fails Validate (Table 10.84 — the condition may not
// reference process data), while the StartEvent itself stays
// constructible with it (the future event-sub-process surface).
func TestStartConditionalRejected(t *testing.T) {
	data.CreateDefaultStates()

	cond := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(true), nil
		})

	// construction is legal — the rejection is placement-scoped.
	start, err := events.NewStartEvent("conditional start",
		events.WithConditionalTrigger(
			events.MustConditionalEventDefinition(cond)))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	p, err := process.New("top-level with conditional start")
	require.NoError(t, err)
	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	err = p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "top-level Start Event")
	require.Contains(t, err.Error(), start.ID())

	// the same graph with a non-conditional trigger validates — the
	// placement gate skips non-conditional start definitions.
	sig, err := events.NewSignal("go", data.MustItemDefinition(
		values.NewVariable(1)))
	require.NoError(t, err)

	plain, err := events.NewStartEvent("signal start",
		events.WithSignalTrigger(events.MustSignalEventDefinition(sig)))
	require.NoError(t, err)

	p2, err := process.New("top-level plain start")
	require.NoError(t, err)
	require.NoError(t, p2.Add(plain))

	end2, err := events.NewEndEvent("end")
	require.NoError(t, err)
	require.NoError(t, p2.Add(end2))

	_, err = flow.Link(plain, end2)
	require.NoError(t, err)

	require.NoError(t, p2.Validate())
}

// TestProcessValidateResourceRoles — SRD-075 T-7: a Process's OWN roles are
// checked at registration, since the Process is not one of its own nodes. A
// directory-mode authorizing role needs a directory the engine doesn't have.
func TestProcessValidateResourceRoles(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	res, err := bpmncommon.NewResource("approvers",
		bpmncommon.MustResourceParameter("level", "int", true))
	require.NoError(t, err)

	owner, err := hi.NewPotentialOwner("process owners", res, nil, nil)
	require.NoError(t, err)

	p, err := process.New("directory role", activities.WithRoles(owner))
	require.NoError(t, err)
	require.NoError(t, p.Add(mustStart(t, "start")))

	err = p.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "process owners")
	require.ErrorContains(t, err, "organizational directory")

	// Roles() exposes what was declared, whichever way it resolves.
	require.Len(t, p.Roles(), 1)

	// a declarative role in the same shape registers cleanly.
	bare, err := hi.NewResourceRole("printer", res, nil, nil)
	require.NoError(t, err)

	p2, err := process.New("declarative role", activities.WithRoles(bare))
	require.NoError(t, err)
	require.NoError(t, p2.Add(mustStart(t, "start")))
	require.NoError(t, p2.Validate())
}

// mustStart builds a plain start event for a minimal valid process.
func mustStart(t *testing.T, name string) *events.StartEvent {
	t.Helper()

	se, err := events.NewStartEvent(name)
	require.NoError(t, err)

	return se
}

// TestProcessLaneSets — SRD-076 T-9: a Process accepts lane sets, exposes them,
// and rejects a lane placing a node it does not hold.
func TestProcessLaneSets(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mine := mustStart(t, "mine")

	foreign, err := events.NewStartEvent("foreign")
	require.NoError(t, err)

	t.Run("carried and exposed", func(t *testing.T) {
		lane, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)
		require.NoError(t, lane.Place(mine))

		ls, err := lanes.NewLaneSet("org", []*lanes.Lane{lane})
		require.NoError(t, err)

		p, err := process.New("laned", lanes.WithLaneSets(ls))
		require.NoError(t, err)
		require.NoError(t, p.Add(mine))

		require.Len(t, p.LaneSets(), 1)
		require.Equal(t, "org", p.LaneSets()[0].Name())
		require.NoError(t, p.Validate())
	})

	t.Run("a lane placing a foreign node fails registration",
		func(t *testing.T) {
			lane, err := lanes.NewLane("outsider", nil, "", nil)
			require.NoError(t, err)
			require.NoError(t, lane.Place(foreign))

			ls, err := lanes.NewLaneSet("org", []*lanes.Lane{lane})
			require.NoError(t, err)

			p, err := process.New("bad-lane", lanes.WithLaneSets(ls))
			require.NoError(t, err)
			require.NoError(t, p.Add(mustStart(t, "own")))

			err = p.Validate()
			require.Error(t, err)
			require.ErrorContains(t, err, "outsider")
		})

	t.Run("a nil lane set is refused at construction", func(t *testing.T) {
		_, err := process.New("nil-set", lanes.WithLaneSets(nil))
		require.Error(t, err)
	})
}

// TestProcessArtifacts — SRD-092 T-5: the Process accepts, orders and exposes
// its artifacts; the collection invariant refuses nils and duplicate ids.
func TestProcessArtifacts(t *testing.T) {
	p, err := process.New("artifacted")
	require.NoError(t, err)

	note := artifacts.MustTextAnnotation("Careful", "",
		foundation.WithID("note"))
	grp := artifacts.MustGroup("critical", foundation.WithID("grp"))

	t.Run("accumulates in order and returns a copy", func(t *testing.T) {
		require.NoError(t, p.AddArtifacts(note))
		require.NoError(t, p.AddArtifacts(grp,
			artifacts.MustAssociation(note, grp, artifacts.DirectionNone,
				foundation.WithID("a1"))))

		arts := p.Artifacts()
		require.Len(t, arts, 3)
		require.Equal(t, "note", arts[0].ID())
		require.Equal(t, "grp", arts[1].ID())
		require.Equal(t, "a1", arts[2].ID())

		// the accessor hands out a copy, not the collection itself
		arts[0] = nil
		require.Equal(t, "note", p.Artifacts()[0].ID())
	})

	t.Run("a nil artifact is refused, the collection unchanged",
		func(t *testing.T) {
			require.ErrorContains(t, p.AddArtifacts(nil), "nil artifact")
			require.Len(t, p.Artifacts(), 3)
		})

	t.Run("a duplicate id is refused", func(t *testing.T) {
		err := p.AddArtifacts(artifacts.MustTextAnnotation("x", "",
			foundation.WithID("note")))
		require.ErrorContains(t, err, "duplicate artifact id")
	})
}

// ioParam builds a required int parameter for the I/O contract tests.
func ioParam(
	t *testing.T, name string, opts ...data.ParameterOption,
) *data.Parameter {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0)),
			data.ReadyDataState),
		opts...)
}

// TestProcessIOSpec — SRD-093 T-2: the declared parameters read back per
// direction, in declaration order; a process declaring none is contract-less.
func TestProcessIOSpec(t *testing.T) {
	t.Run("declared parameters read back per direction", func(t *testing.T) {
		p, err := process.New("pricing",
			data.WithInputs(ioParam(t, "subtotal")),
			data.WithInputs(ioParam(t, "discount", data.Optional())),
			data.WithOutputs(ioParam(t, "total")))
		require.NoError(t, err)

		ios := p.IOSpec()
		require.NotNil(t, ios)

		ins := ios.InputSet()
		require.Len(t, ins, 2)
		require.Equal(t, "subtotal", ins[0].Name())
		require.Equal(t, "discount", ins[1].Name())
		require.True(t, ins[1].IsOptional())

		outs := ios.OutputSet()
		require.Len(t, outs, 1)
		require.Equal(t, "total", outs[0].Name())

		require.NoError(t, p.Validate())
	})

	t.Run("a process declaring no parameter is contract-less",
		func(t *testing.T) {
			p, err := process.New("plain")
			require.NoError(t, err)
			require.Nil(t, p.IOSpec())
		})

	t.Run("a nil parameter refuses construction", func(t *testing.T) {
		_, err := process.New("bad", data.WithInputs(nil))
		require.ErrorContains(t, err, "nil parameter")
	})
}

// TestProcessIONameSpace — SRD-093 T-3: the root scope has one namespace
// (ADR-040 §2.6) — a parameter named like a property, a data object, or a
// parameter of the other direction fails Validate naming both parties.
func TestProcessIONameSpace(t *testing.T) {
	t.Run("a parameter named like a property", func(t *testing.T) {
		require.NoError(t, data.CreateDefaultStates())

		prop := data.MustProperty("total",
			data.MustItemDefinition(values.NewVariable(0)), data.ReadyDataState)

		p, err := process.New("clash",
			data.WithProperties(prop),
			data.WithOutputs(ioParam(t, "total")))
		require.NoError(t, err)

		err = p.Validate()
		require.ErrorContains(t, err, `"total"`)
		require.ErrorContains(t, err, "process property")
	})

	t.Run("a parameter named like a data object", func(t *testing.T) {
		require.NoError(t, data.CreateDefaultStates())

		do, err := dataobjects.New("order",
			data.MustItemDefinition(values.NewVariable(1)), data.ReadyDataState)
		require.NoError(t, err)

		p, err := process.New("clash", data.WithInputs(ioParam(t, "order")))
		require.NoError(t, err)
		require.NoError(t, p.Add(do))

		err = p.Validate()
		require.ErrorContains(t, err, `"order"`)
		require.ErrorContains(t, err, "data object")
	})

	t.Run("an input and an output of the same name", func(t *testing.T) {
		p, err := process.New("clash",
			data.WithInputs(ioParam(t, "amount")),
			data.WithOutputs(ioParam(t, "amount")))
		require.NoError(t, err)

		err = p.Validate()
		require.ErrorContains(t, err, `"amount"`)
		require.ErrorContains(t, err, "INPUT parameter of the same name")
	})

	t.Run("two inputs of the same name — the specification's own check",
		func(t *testing.T) {
			p, err := process.New("clash",
				data.WithInputs(ioParam(t, "x"), ioParam(t, "x")))
			require.NoError(t, err)
			require.Error(t, p.Validate())
		})
}
