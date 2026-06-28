package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// buildProcess assembles the abort-from-one-branch demo:
//
//	start → split ─┬─ fraud-check ──> terminate-end   (kills the instance)
//	               └─ process-payment ──> payment-done
//
// The fraud check finishes first and reaches a Terminate End Event, which abnormally
// terminates the whole instance — cancelling the in-flight payment mid-charge.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("terminate-end-event")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	split, err := gateways.NewParallelGateway(gateways.WithDirection(gateways.Diverging))
	if err != nil {
		return nil, fmt.Errorf("split: %w", err)
	}

	fraudCheck, err := serviceTask("fraud-check", fraudCheckOp)
	if err != nil {
		return nil, err
	}

	payment, err := serviceTask("process-payment", paymentOp)
	if err != nil {
		return nil, err
	}

	termEd, err := events.NewTerminateEventDefinition()
	if err != nil {
		return nil, fmt.Errorf("terminate def: %w", err)
	}

	terminate, err := events.NewEndEvent("terminate-order",
		events.WithTerminateTrigger(termEd))
	if err != nil {
		return nil, fmt.Errorf("terminate-end: %w", err)
	}

	paymentDone, err := events.NewEndEvent("payment-done")
	if err != nil {
		return nil, fmt.Errorf("payment-done: %w", err)
	}

	for _, e := range []flow.Element{
		start, split, fraudCheck, payment, terminate, paymentDone,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, split},
		{split, fraudCheck},
		{fraudCheck, terminate},
		{split, payment},
		{payment, paymentDone},
	} {
		if _, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget)); err != nil {
			return nil, fmt.Errorf("link: %w", err)
		}
	}

	return proc, nil
}

// serviceTask builds a ServiceTask from an operation constructor.
func serviceTask(
	id string, build func() (service.Operation, error),
) (*activities.ServiceTask, error) {
	op, err := build()
	if err != nil {
		return nil, fmt.Errorf("op %q: %w", id, err)
	}

	st, err := activities.NewServiceTask(id, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("service task %q: %w", id, err)
	}

	return st, nil
}
