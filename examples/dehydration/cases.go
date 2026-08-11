package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// the timer threshold the engine applies: a one-shot timer further out than
// this is worth a checkpoint-and-rebuild round trip, a nearer one is not.
const threshold = time.Hour

type demo struct {
	eng    *thresher.Thresher
	clk    *clocktest.Clock
	broker *membroker.Broker
	inbox  *inbox
	watch  *residency

	done map[string]chan string
	proc map[string]*process.Process
}

type demoCase struct {
	run   func() error
	title string
	note  string
}

// registerAll builds and registers one process per case.
func (d *demo) registerAll() error {
	d.done = map[string]chan string{}
	d.proc = map[string]*process.Process{}

	build := map[string]func(chan string) *process.Process{
		// FAR past the threshold — this one releases.
		"timer-long": func(c chan string) *process.Process {
			return timerProcess("timer-long", d.clk, 48*time.Hour, c)
		},
		// INSIDE the threshold — deliberately stays resident.
		"timer-short": func(c chan string) *process.Process {
			return timerProcess("timer-short", d.clk, 5*time.Minute, c)
		},
		"message": func(c chan string) *process.Process {
			return messageProcess("message", "payment received", c)
		},
		"signal": func(c chan string) *process.Process {
			return signalProcess("signal", "go-live", c)
		},
		"user-task": func(c chan string) *process.Process {
			return userTaskProcess("user-task", c)
		},
		"gateway": func(c chan string) *process.Process {
			return gatewayProcess("gateway", "cancel",
				d.clk, 48*time.Hour, c)
		},
	}

	for key, mk := range build {
		ch := make(chan string, 4)
		p := mk(ch)

		if _, err := d.eng.RegisterProcess(p); err != nil {
			return fmt.Errorf("registering %s: %w", key, err)
		}

		d.done[key] = ch
		d.proc[key] = p
	}

	return nil
}

func (d *demo) cases() []demoCase {
	return []demoCase{
		{
			title: "1. timer, 48h out — RELEASES, then wakes on the deadline",
			note:  "far past the 1h threshold, so it is worth releasing for",
			run:   d.timerLong,
		},
		{
			title: "2. timer, 5m out — stays RESIDENT, fires normally",
			note:  "inside the threshold: a checkpoint round trip would cost more than it saves",
			run:   d.timerShort,
		},
		{
			title: "3. message — RELEASES, woken by a correlated message",
			note:  "a receive is a pure wait, so there is no threshold",
			run:   d.message,
		},
		{
			title: "4. signal — RELEASES, woken by a broadcast",
			note:  "same holder as a message, minus the conversation",
			run:   d.signal,
		},
		{
			title: "5. human task — RELEASES, woken by Complete",
			note:  "the task lives in the distributor's inbox, so the instance need not stay",
			run:   d.userTask,
		},
		{
			title: "6. event-based gateway — RELEASES a SET, one arm wins",
			note:  "the gate holds both arms; the winner fires, the loser is withdrawn",
			run:   d.gateway,
		},
	}
}

// --- the cases ---------------------------------------------------------------

func (d *demo) timerLong() error {
	before := d.watch.dehydrations()

	if _, err := d.eng.StartLatest("timer-long"); err != nil {
		return err
	}

	if !d.watch.awaitDehydration(before, 3*time.Second) {
		return errors.New("a 48h timer should have released the instance")
	}

	// nothing is running now — moving time is what brings it back.
	d.clk.Advance(49 * time.Hour)

	return d.expect("timer-long", "woken by its deadline")
}

func (d *demo) timerShort() error {
	before := d.watch.dehydrations()

	if _, err := d.eng.StartLatest("timer-short"); err != nil {
		return err
	}

	// give it the same chance to release; it must NOT take it.
	if d.watch.awaitDehydration(before, 500*time.Millisecond) {
		return fmt.Errorf("a timer inside the %s threshold must stay resident",
			threshold)
	}

	fmt.Println("      · stayed resident (no dehydration fact)")

	d.clk.Advance(10 * time.Minute)

	return d.expect("timer-short", "fired from memory, never released")
}

func (d *demo) message() error {
	before := d.watch.dehydrations()

	if _, err := d.eng.StartLatest("message"); err != nil {
		return err
	}

	if !d.watch.awaitDehydration(before, 3*time.Second) {
		return errors.New("a message catch should have released the instance")
	}

	if err := d.broker.Publish(context.Background(), messaging.Envelope{
		Name: "payment received", Payload: "ORD-1"}); err != nil {
		return err
	}

	return d.expect("message", "woken by the message")
}

func (d *demo) signal() error {
	before := d.watch.dehydrations()

	if _, err := d.eng.StartLatest("signal"); err != nil {
		return err
	}

	if !d.watch.awaitDehydration(before, 3*time.Second) {
		return errors.New("a signal catch should have released the instance")
	}

	if err := d.eng.PropagateEvent(
		context.Background(), signalDef("go-live")); err != nil {
		return err
	}

	return d.expect("signal", "woken by the broadcast")
}

func (d *demo) userTask() error {
	before := d.watch.dehydrations()

	if _, err := d.eng.StartLatest("user-task"); err != nil {
		return err
	}

	if !d.watch.awaitDehydration(before, 3*time.Second) {
		return errors.New("a human task should have released the instance")
	}

	taskID, ok := d.inbox.awaitTask(3 * time.Second)
	if !ok {
		return errors.New("the task was never announced")
	}

	// Claiming takes exclusive hold of the task so no other candidate can work it
	// in parallel, and completion is then the holder's alone. Note what does NOT
	// happen here: the claim leaves the instance released. Ownership is recorded
	// beside the task, not inside the instance, so a claim during a long human
	// wait costs no hydration at all.
	if err := d.eng.Claim(context.Background(), taskID,
		actor{id: "operator"}); err != nil {
		return err
	}

	// The caller does not know or care that the instance released: acting on
	// the task hydrates it, and the id it holds survives the round trip.
	decision := []data.Data{
		data.MustParameter("decision",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	if err := d.eng.Complete(context.Background(), taskID,
		actor{id: "operator"}, decision); err != nil {
		return err
	}

	return d.expect("user-task", "woken by the completion")
}

func (d *demo) gateway() error {
	before := d.watch.dehydrations()

	if _, err := d.eng.StartLatest("gateway"); err != nil {
		return err
	}

	if !d.watch.awaitDehydration(before, 3*time.Second) {
		return errors.New("a gateway with two holdable arms should have released")
	}

	if err := d.broker.Publish(context.Background(), messaging.Envelope{
		Name: "cancel", Payload: "stop"}); err != nil {
		return err
	}

	if err := d.expect("gateway", "woken down the winning arm"); err != nil {
		return err
	}

	// the losing timer arm was withdrawn with the rest of the set: pushing the
	// clock far past its deadline fires nothing.
	d.clk.Advance(72 * time.Hour)

	if note, fired := await(d.done["gateway"], 300*time.Millisecond); fired {
		return fmt.Errorf("the losing arm still fired: %s", note)
	}

	fmt.Println("      · losing arm withdrawn (its deadline fires nothing)")

	return nil
}

// expect reads the flow's own confirmation that it got past the wait.
func (d *demo) expect(key, what string) error {
	note, ok := await(d.done[key], 5*time.Second)
	if !ok {
		return fmt.Errorf("the flow never passed its wait (%s)", what)
	}

	fmt.Printf("      ✓ %s — %s\n", what, note)

	return nil
}
