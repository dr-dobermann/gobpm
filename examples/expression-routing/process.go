package main

import (
	"fmt"

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

	intake, err := printTask(ran, "intake", "  ▶ intake: checking the order")
	if err != nil {
		return nil, err
	}

	fxAudit, err := printTask(ran, "fx-audit",
		`  ▶ fx-audit: rates["EUR"] < 1.2 (the ##GoExpr functor lane)`)
	if err != nil {
		return nil, err
	}

	xor, err := gateways.NewExclusiveGateway()
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}

	urgent, err := printTask(ran, "urgent",
		"  ▶ urgent: the deadline is near (the lite time() branch)")
	if err != nil {
		return nil, err
	}

	standard, err := printTask(ran, "standard",
		"  ▶ standard: no rush (the gateway's default flow)")
	if err != nil {
		return nil, err
	}

	approve, err := approveTask()
	if err != nil {
		return nil, err
	}

	endApproved, err := events.NewEndEvent("end-approved")
	if err != nil {
		return nil, fmt.Errorf("end-approved: %w", err)
	}

	endStandard, err := events.NewEndEvent("end-standard")
	if err != nil {
		return nil, fmt.Errorf("end-standard: %w", err)
	}

	endAudited, err := events.NewEndEvent("end-audited")
	if err != nil {
		return nil, fmt.Errorf("end-audited: %w", err)
	}

	for _, e := range []flow.Element{
		start, intake, fxAudit, xor, urgent, standard, approve,
		endApproved, endStandard, endAudited,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %q: %w", e.Name(), err)
		}
	}

	if _, err := flow.Link(start, intake); err != nil {
		return nil, fmt.Errorf("link start: %w", err)
	}

	// SITE 1 — task flows, mixed engines at one selection point.
	premiumCond, err := lite.Cond(
		`order.total > 100 and order.customer.tier == "vip"`)
	if err != nil {
		return nil, fmt.Errorf("premium condition: %w", err)
	}

	if _, err := flow.Link(intake, xor,
		flow.WithCondition(premiumCond)); err != nil {
		return nil, fmt.Errorf("link intake→xor: %w", err)
	}

	if _, err := flow.Link(intake, fxAudit,
		flow.WithCondition(eurRateOK())); err != nil {
		return nil, fmt.Errorf("link intake→fx-audit: %w", err)
	}

	// SITE 2 — the gateway: a lite time() branch + the default flow.
	urgentCond, err := lite.Cond(
		`deadline < time("2026-12-31T00:00:00Z")`)
	if err != nil {
		return nil, fmt.Errorf("urgent condition: %w", err)
	}

	if _, err := flow.Link(xor, urgent,
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
