package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// Every process here is start -> <the wait> -> arrived -> end. Only the WAIT
// differs, because the wait is what decides whether the instance may release
// its goroutines.

// timerProcess builds a process waiting `in` from the moment the timer is
// ARMED. Whether it dehydrates depends entirely on how far out that is — see
// the two timer cases in cases.go.
func timerProcess(
	key string, clk clock.Clock, in time.Duration, done chan<- string,
) *process.Process {
	p := newProcess(key)

	wait := must(events.NewIntermediateCatchEvent("wait",
		timerDef(clk, in), foundation.WithID(key+"-wait")))

	return wire(p, key, wait, done)
}

// messageProcess waits on a mid-flow message catch — a pure receive, so it
// releases the instance unconditionally.
func messageProcess(key, msgName string, done chan<- string) *process.Process {
	p := newProcess(key)

	wait := must(events.NewIntermediateCatchEvent("wait",
		messageDef(msgName), foundation.WithID(key+"-wait")))

	return wire(p, key, wait, done)
}

// signalProcess waits on a broadcast signal.
func signalProcess(key, sigName string, done chan<- string) *process.Process {
	p := newProcess(key)

	wait := must(events.NewIntermediateCatchEvent("wait",
		signalDef(sigName), foundation.WithID(key+"-wait")))

	return wire(p, key, wait, done)
}

// userTaskProcess waits on a human task. The task lives in the distributor's
// inbox, which is precisely why the instance need not stay in memory.
func userTaskProcess(key string, done chan<- string) *process.Process {
	p := newProcess(key)

	wait := must(activities.NewUserTask("approve",
		activities.WithCandidateUsers("operator"),
		activities.WithOutput("decision", "string", true),
		activities.WithoutParams(),
		foundation.WithID(key+"-wait")))

	return wire(p, key, wait, done)
}

// gatewayProcess races a long timer against a message on an Event-Based
// Gateway. The gate is ONE wait node holding a SET of holds — it releases the
// instance only because BOTH arms are holdable.
func gatewayProcess(
	key, msgName string, clk clock.Clock, in time.Duration, done chan<- string,
) *process.Process {
	p := newProcess(key)

	gate := must(gateways.NewEventBasedGateway(
		gateways.WithDirection(gateways.Diverging),
		foundation.WithID(key+"-gate")))

	timerArm := must(events.NewIntermediateCatchEvent("on-timeout",
		timerDef(clk, in), foundation.WithID(key+"-timerarm")))
	msgArm := must(events.NewIntermediateCatchEvent("on-message",
		messageDef(msgName), foundation.WithID(key+"-msgarm")))

	timedOut := arrivedTask(key+"-timeout", "the timer arm won", done)
	messaged := arrivedTask(key+"-message", "the message arm won", done)

	endT := must(events.NewEndEvent("endT", foundation.WithID(key+"-endT")))
	endM := must(events.NewEndEvent("endM", foundation.WithID(key+"-endM")))
	start := must(events.NewStartEvent("start", foundation.WithID(key+"-start")))

	add(p, start, gate, timerArm, msgArm, timedOut, messaged, endT, endM)

	link(start, gate)
	link(gate, timerArm)
	link(gate, msgArm)
	link(timerArm, timedOut)
	link(msgArm, messaged)
	link(timedOut, endT)
	link(messaged, endM)

	return p
}

// wire completes the common start -> wait -> arrived -> end shape.
func wire(
	p *process.Process, key string, wait flow.Element, done chan<- string,
) *process.Process {
	start := must(events.NewStartEvent("start", foundation.WithID(key+"-start")))
	arrived := arrivedTask(key+"-arrived", "the wait fired", done)
	end := must(events.NewEndEvent("end", foundation.WithID(key+"-end")))

	add(p, start, wait, arrived, end)

	link(start, wait)
	link(wait, arrived)
	link(arrived, end)

	return p
}

// arrivedTask reports on the channel that the flow got past its wait.
func arrivedTask(id, note string, done chan<- string) *activities.ServiceTask {
	op := must(gooper.New(id,
		func(context.Context, service.DataReader,
			*data.ItemDefinition) (*data.ItemDefinition, error) {
			done <- note

			return nil, nil
		}))

	return must(activities.NewServiceTask(id, op,
		activities.WithoutParams(), foundation.WithID(id)))
}

// --- definition builders -----------------------------------------------------

// timerDef resolves its deadline when the timer is ARMED, not when the model
// is built: the expression runs on every arm, so a rebuilt instance re-arms
// against the clock as it stands then.
func timerDef(clk clock.Clock, in time.Duration) flow.EventDefinition {
	expr := must(goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(clk.Now().Add(in)), nil
		}))

	return must(events.NewTimerEventDefinition(expr, nil, nil))
}

func messageDef(name string) flow.EventDefinition {
	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage(name, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("payload"))), nil)
}

func signalDef(name string) flow.EventDefinition {
	return must(events.NewSignalEventDefinition(
		must(events.NewSignal(name, nil))))
}

// --- tiny helpers ------------------------------------------------------------

func newProcess(key string) *process.Process {
	return must(process.New(key, foundation.WithID(key)))
}

func add(p *process.Process, ee ...flow.Element) {
	for _, e := range ee {
		if err := p.Add(e); err != nil {
			panic(fmt.Sprintf("adding %q: %v", e.Name(), err))
		}
	}
}

// link wires two elements, panicking like must: an element that cannot carry a
// sequence flow is a bug in the example, not a runtime condition. It takes
// flow.Element so the assertion — and its message — live here rather than at
// each call site, where a bare `x.(flow.SequenceTarget)` would panic with no
// indication of which element was wrong.
func link(src, tgt flow.Element) {
	s, ok := src.(flow.SequenceSource)
	if !ok {
		panic(fmt.Sprintf("%q is not a sequence source", src.Name()))
	}

	t, ok := tgt.(flow.SequenceTarget)
	if !ok {
		panic(fmt.Sprintf("%q is not a sequence target", tgt.Name()))
	}

	if _, err := flow.Link(s, t); err != nil {
		panic(fmt.Sprintf("linking: %v", err))
	}
}

// must is example-only sugar: a model that will not build is a bug in the
// example itself, not a runtime condition worth handling. Library code uses
// the error-returning constructors instead.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}

	return v
}
