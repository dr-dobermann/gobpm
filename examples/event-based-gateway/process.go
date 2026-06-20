package main

import (
	"context"
	"fmt"
	"time"

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

// approvalMessage is the message the approval arm waits for; the demo publishes it.
const approvalMessage = "approval"

// buildProcess builds an Event-Based gateway deferred choice:
//
//	start → event-gate ─┬→ catch(approval message) → approved → end
//	                    └→ catch(10s timeout timer) → timedOut → end
//
// The gate subscribes to BOTH arms and routes the token down whichever fires first;
// the other subscription is dropped. The demo publishes the approval message, so the
// approval arm wins before the timer — the timer is the self-terminating fallback.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("event-based-gateway")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	gate, err := gateways.NewEventBasedGateway(
		gateways.WithDirection(gateways.Diverging))
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}

	approvalArm, err := messageCatch("approval", approvalMessage)
	if err != nil {
		return nil, err
	}

	timeoutArm, err := timerCatch("timeout", 10*time.Second)
	if err != nil {
		return nil, err
	}

	approved, err := printTask("approved",
		"  ✓ approval arrived first → order approved")
	if err != nil {
		return nil, err
	}

	timedOut, err := printTask("timedOut",
		"  ✓ 10s elapsed first → order timed out")
	if err != nil {
		return nil, err
	}

	endA, err := events.NewEndEvent("end-approved")
	if err != nil {
		return nil, fmt.Errorf("end-approved: %w", err)
	}

	endT, err := events.NewEndEvent("end-timeout")
	if err != nil {
		return nil, fmt.Errorf("end-timeout: %w", err)
	}

	for _, e := range []flow.Element{
		start, gate, approvalArm, timeoutArm, approved, timedOut, endA, endT,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, gate},
		{gate, approvalArm}, {gate, timeoutArm},
		{approvalArm, approved}, {timeoutArm, timedOut},
		{approved, endA}, {timedOut, endT},
	} {
		if _, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget)); err != nil {
			return nil, fmt.Errorf("link: %w", err)
		}
	}

	return proc, nil
}

// messageCatch builds an intermediate catch event waiting for the named message.
func messageCatch(id, msgName string) (*events.IntermediateCatchEvent, error) {
	def, err := events.NewMessageEventDefinition(
		bpmncommon.MustMessage(msgName, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID(id+"_in"))),
		nil)
	if err != nil {
		return nil, fmt.Errorf("message def %q: %w", msgName, err)
	}

	ice, err := events.NewIntermediateCatchEvent(id, def)
	if err != nil {
		return nil, fmt.Errorf("catch %q: %w", id, err)
	}

	return ice, nil
}

// timerCatch builds an intermediate catch event firing after d from now.
func timerCatch(id string, d time.Duration) (*events.IntermediateCatchEvent, error) {
	when := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(time.Now().Add(d))),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(d)), nil
		},
		foundation.WithID(id+"-at"))

	def, err := events.NewTimerEventDefinition(when, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("timer def %q: %w", id, err)
	}

	ice, err := events.NewIntermediateCatchEvent(id, def)
	if err != nil {
		return nil, fmt.Errorf("catch %q: %w", id, err)
	}

	return ice, nil
}

// printTask builds a ServiceTask whose Go functor prints msg when the arm runs.
func printTask(id, msg string) (*activities.ServiceTask, error) {
	op, err := gooper.New(id,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println(msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("op %q: %w", id, err)
	}

	task, err := activities.NewServiceTask(id, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}

	return task, nil
}
