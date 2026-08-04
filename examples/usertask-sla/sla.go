package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// sla is the budget the approval is expected to fit into. The marks below are
// fractions of it, so changing this one value moves all three.
const sla = 2 * time.Second

// slaMark is one warning on the approval's clock: how far into the budget it
// fires, the notification task's id, and the line it prints.
type slaMark struct {
	id  string
	msg string
	at  time.Duration
}

var slaMarks = []slaMark{
	{"halfway", "50% of the SLA is gone — the approval is still open", sla / 2},
	{"urgent", "90% of the SLA is gone — this needs attention now", sla * 9 / 10},
	{"escalated", "SLA breached — escalating to the supervisor", sla},
}

// durationExpr builds the timer's timeDuration: a RELATIVE deadline, measured
// from the moment the boundary arms.
//
// Before SRD-077 this could not be expressed at all — the constructor refused a
// timeDuration without a timeCycle beside it — so relative timers had to be
// faked with a date expression computing time.Now().Add(d) at evaluation time
// (see examples/boundary-events). That workaround bypasses the engine's
// injected Clock; this does not.
func durationExpr(id string, d time.Duration) data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(time.Duration(0))),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(d), nil
		},
		foundation.WithID(id+"-after"))
}

// slaBoundary attaches one NON-INTERRUPTING timer boundary to host.
// Non-interrupting is the whole point: the warning forks a parallel token and
// leaves the guarded UserTask running, so the approval survives its own SLA
// alarms. An interrupting boundary would cancel the work it was warning about.
func slaBoundary(
	id string, host flow.ActivityNode, after time.Duration,
) (*events.BoundaryEvent, error) {
	def, err := events.NewTimerEventDefinition(
		nil, nil, durationExpr(id, after))
	if err != nil {
		return nil, fmt.Errorf("timer def %q: %w", id, err)
	}

	be, err := events.NewBoundaryEvent(id+"-timer", host, def, false)
	if err != nil {
		return nil, fmt.Errorf("boundary %q: %w", id, err)
	}

	return be, nil
}

// banner prints the shape of the run before it starts, so the printed
// timeline can be read against what the process actually does.
func banner() {
	fmt.Printf(`
	  usertask-sla (SLA warnings on a human task):
	    start → (approve-invoice: UserTask) → end-approved
	              ╎ three NON-INTERRUPTING timer boundaries, timeDuration only
	              ├╌ %-6v → halfway    (50%% of the SLA)
	              ├╌ %-6v → urgent     (90%%)
	              └╌ %-6v → escalated  (100%% — breached)
	            operator holds the task %v, so all three fire first.
	
	`, slaMarks[0].at, slaMarks[1].at, slaMarks[2].at, overrun)
}
