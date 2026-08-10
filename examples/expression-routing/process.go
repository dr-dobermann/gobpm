package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the order flow — the expression layer at THREE
// consumer sites, two engines mixed with zero extra registration:
//
//		start → [intake] ─ lite ───→ (XOR) ─ lite ────→ [urgent] → [approve] → end
//		            │                  └─── default ──→ [standard] → end
//		            └───── goexpr ─→ [fx-audit] → end
//
//	  - SITE 1, task flows: intake's outgoing flows mix a gobpm:lite TEXT
//	    condition with a ##GoExpr functor condition — one selection point,
//	    each expression routed to its own engine;
//	  - SITE 2, the gateway: the XOR branches on a lite time() comparison
//	    against the deadline, with a default flow;
//	  - SITE 3, user-task authorization: approve's assignee is computed by
//	    a lite string expression (tasks.go).
func buildProcess(ran *pathSet) (*process.Process, error) {
	props, err := orderData()
	if err != nil {
		return nil, err
	}

	proc, err := process.New("expression-routing",
		data.WithProperties(props...))
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// The four route destinations. Each prints when it runs, so the console
	// shows which way the conditions actually sent the token.
	tasks, err := printTasks(ran, [][2]string{
		{"intake", "  ▶ intake: checking the order"},
		{"fx-audit", `  ▶ fx-audit: rates["EUR"] < 1.2 (the ##GoExpr functor lane)`},
		{"urgent", "  ▶ urgent: the deadline is near (the lite time() branch)"},
		{"standard", "  ▶ standard: no rush (the gateway's default flow)"},
	})
	if err != nil {
		return nil, err
	}

	intake, fxAudit, urgent, standard := tasks[0], tasks[1], tasks[2], tasks[3]

	xor, err := gateways.NewExclusiveGateway()
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}

	approve, err := approveTask()
	if err != nil {
		return nil, err
	}

	ends, err := endEvents("end-approved", "end-standard", "end-audited")
	if err != nil {
		return nil, err
	}

	endApproved, endStandard, endAudited := ends[0], ends[1], ends[2]

	if addErr := addAll(proc,
		start, intake, fxAudit, xor, urgent, standard, approve,
		endApproved, endStandard, endAudited,
	); addErr != nil {
		return nil, addErr
	}

	if _, err = flow.Link(start, intake); err != nil {
		return nil, fmt.Errorf("link start: %w", err)
	}

	// SITE 1 — task flows, mixed engines at one selection point.
	premiumCond, urgentCond, err := routeConditions()
	if err != nil {
		return nil, err
	}

	if _, err = flow.Link(intake, xor,
		flow.WithCondition(premiumCond)); err != nil {
		return nil, fmt.Errorf("link intake→xor: %w", err)
	}

	if _, err = flow.Link(intake, fxAudit,
		flow.WithCondition(eurRateOK())); err != nil {
		return nil, fmt.Errorf("link intake→fx-audit: %w", err)
	}

	// SITE 2 — the gateway: a lite time() branch + the default flow.
	if _, err = flow.Link(xor, urgent,
		flow.WithCondition(urgentCond)); err != nil {
		return nil, fmt.Errorf("link xor→urgent: %w", err)
	}

	df, err := flow.Link(xor, standard)
	if err != nil {
		return nil, fmt.Errorf("link xor→standard: %w", err)
	}

	if err := xor.UpdateDefaultFlow(df); err != nil {
		return nil, fmt.Errorf("set default flow: %w", err)
	}

	// SITE 3 — the lite-assigned approval, then the tails.
	for _, pair := range [][2]flow.Element{
		{urgent, approve}, {approve, endApproved},
		{standard, endStandard}, {fxAudit, endAudited},
	} {
		src, _ := pair[0].(flow.SequenceSource)
		trg, _ := pair[1].(flow.SequenceTarget)

		if _, err := flow.Link(src, trg); err != nil {
			return nil, fmt.Errorf("link %q→%q: %w",
				pair[0].Name(), pair[1].Name(), err)
		}
	}

	return proc, nil
}

// endEvents builds one End Event per name. The three terminal states are what
// this example distinguishes, so they are constructed together rather than
// spelled out one at a time in the middle of the model.
func endEvents(names ...string) ([]*events.EndEvent, error) {
	ends := make([]*events.EndEvent, 0, len(names))

	for _, n := range names {
		e, err := events.NewEndEvent(n)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}

		ends = append(ends, e)
	}

	return ends, nil
}

// printTasks builds one print task per {name, message} pair, in order.
func printTasks(ran *pathSet, specs [][2]string) ([]*activities.ServiceTask, error) {
	tasks := make([]*activities.ServiceTask, 0, len(specs))

	for _, sp := range specs {
		t, err := printTask(ran, sp[0], sp[1])
		if err != nil {
			return nil, err
		}

		tasks = append(tasks, t)
	}

	return tasks, nil
}

// routeConditions builds the two gobpm:lite conditions the routing turns on:
// the premium test on the task flow out of intake, and the deadline test on
// the gateway. They are built together because they are the decision this
// example exists to show — the rest of the model is scaffolding around them.
func routeConditions() (premium, urgent data.FormalExpression, err error) {
	premium, err = lite.Cond(`order.total > 100 and order.customer.tier == "vip"`)
	if err != nil {
		return nil, nil, fmt.Errorf("premium condition: %w", err)
	}

	urgent, err = lite.Cond(`deadline < time("2026-12-31T00:00:00Z")`)
	if err != nil {
		return nil, nil, fmt.Errorf("urgent condition: %w", err)
	}

	return premium, urgent, nil
}

// addAll registers every element on the process, naming the one that failed.
func addAll(proc *process.Process, elems ...flow.Element) error {
	for _, e := range elems {
		if err := proc.Add(e); err != nil {
			return fmt.Errorf("add %q: %w", e.Name(), err)
		}
	}

	return nil
}
