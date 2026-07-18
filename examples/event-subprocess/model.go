package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles a checkout whose payment wait is guarded by an
// interrupting Event Sub-Process:
//
//	start → checkout → await-payment[ p-start → awaitPay(⏳) → charge → p-end
//	                                  + (timer⚡) timeout-fired → releaseHold ]
//	       → notify → end
//
// The payment message is never sent, so the timer fires first: the handler
// cancels the blocked wait, runs releaseHold, and — reaching its End without
// re-throwing — absorbs the event, so the parent resumes on its normal flow.
func buildProcess() (*process.Process, error) {
	timeout, err := buildTimeoutHandler()
	if err != nil {
		return nil, err
	}

	await, err := activities.NewSubProcess("await-payment")
	if err != nil {
		return nil, fmt.Errorf("create await-payment: %w", err)
	}

	pStart, err := events.NewStartEvent("p-start")
	if err != nil {
		return nil, err
	}
	pay, err := awaitPayment("awaitPay")
	if err != nil {
		return nil, err
	}
	charge, err := step("charge")
	if err != nil {
		return nil, err
	}
	pEnd, err := events.NewEndEvent("p-end")
	if err != nil {
		return nil, err
	}
	if err := wire(await,
		[]flow.Element{pStart, pay, charge, pEnd, timeout},
		[2]flow.Element{pStart, pay},
		[2]flow.Element{pay, charge},
		[2]flow.Element{charge, pEnd}); err != nil {
		return nil, fmt.Errorf("assemble await-payment: %w", err)
	}

	return assembleProcess(await)
}

// buildTimeoutHandler builds the interrupting Event Sub-Process:
// timer-start → releaseHold → end.
func buildTimeoutHandler() (*activities.SubProcess, error) {
	timeout, err := activities.NewSubProcess("payment-timeout",
		activities.WithTriggeredByEvent())
	if err != nil {
		return nil, fmt.Errorf("create payment-timeout: %w", err)
	}

	tStart, err := events.NewStartEvent("timeout-fired",
		events.WithTimerTrigger(timeoutTimer()))
	if err != nil {
		return nil, err
	}
	release, err := step("releaseHold")
	if err != nil {
		return nil, err
	}
	tEnd, err := events.NewEndEvent("timeout-end")
	if err != nil {
		return nil, err
	}
	if err := wire(timeout,
		[]flow.Element{tStart, release, tEnd},
		[2]flow.Element{tStart, release},
		[2]flow.Element{release, tEnd}); err != nil {
		return nil, fmt.Errorf("assemble payment-timeout: %w", err)
	}

	return timeout, nil
}

// assembleProcess wires the top-level flow start → checkout → await → notify → end.
func assembleProcess(await *activities.SubProcess) (*process.Process, error) {
	proc, err := process.New("event-subprocess")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}
	checkout, err := step("checkout")
	if err != nil {
		return nil, err
	}
	notify, err := step("notify")
	if err != nil {
		return nil, err
	}
	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, err
	}
	if err := wire(proc,
		[]flow.Element{start, checkout, await, notify, end},
		[2]flow.Element{start, checkout},
		[2]flow.Element{checkout, await},
		[2]flow.Element{await, notify},
		[2]flow.Element{notify, end}); err != nil {
		return nil, fmt.Errorf("assemble process: %w", err)
	}

	return proc, nil
}
