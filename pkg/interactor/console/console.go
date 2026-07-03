// Package console provides a batteries-included console TaskDistributor that
// drives a parked UserTask through the existing console renderer
// (pkg/model/hinteraction/consinp): on announcement it Takes the task, renders
// its form to collect the outputs, and Completes it. It is a reference/checking
// implementation — wire it via thresher.WithTaskDistributor and UserTasks are
// completed from the console (ADR-020 §2.2).
package console

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

// Engine is the slice of the engine (the Thresher) a Driver needs to act on a
// task: authorize+read (Take) and authorize+validate+resume (Complete).
type Engine interface {
	Take(
		ctx context.Context, taskID string, actor hi.Actor,
	) (interactor.TaskView, error)
	Complete(
		ctx context.Context, taskID string, actor hi.Actor, outputs []data.Data,
	) error
}

// Driver is a TaskDistributor that console-drives each parked UserTask on a
// background goroutine (so the instance loop is never blocked). It is bound to an
// Engine and acts as a single Actor. Build it, pass it to
// thresher.WithTaskDistributor, then Bind the engine before starting instances.
type Driver struct {
	engine Engine
	actor  hi.Actor
	w      io.Writer
}

// New builds a console Driver acting as actor and writing progress to w (nil w
// defaults to os.Stdout). The engine is attached later via Bind.
func New(actor hi.Actor, w io.Writer) *Driver {
	if w == nil {
		w = os.Stdout
	}

	return &Driver{actor: actor, w: w}
}

// Bind attaches the engine the Driver drives (the Thresher). It must be called
// after thresher.New (which takes the Driver) and before instances start.
func (d *Driver) Bind(e Engine) {
	d.engine = e
}

// Distribute announces a parked task and drives it to completion on a background
// goroutine — Take, render the form, Complete — so the instance loop that called
// Distribute is not blocked (and cannot deadlock on the re-entrant Take). The
// interaction detaches from the (short, timeout-bounded) Distribute context via
// WithoutCancel — a human may take arbitrarily long — while keeping its values.
func (d *Driver) Distribute(
	ctx context.Context,
	task interactor.TaskInfo,
) error {
	d.printf("task available: id=%s node=%s\n", task.TaskID, task.NodeID)

	go d.drive(context.WithoutCancel(ctx), task.TaskID)

	return nil
}

// Withdraw prints that a task is no longer completable.
func (d *Driver) Withdraw(_ context.Context, taskID string) error {
	d.printf("task withdrawn: id=%s\n", taskID)

	return nil
}

// drive Takes the task, renders its first form to collect the outputs, and
// Completes it. Errors (unauthorized, invalid outputs) are printed — the task
// stays parked and a real driver would retry; here it simply reports.
func (d *Driver) drive(ctx context.Context, taskID string) {
	view, err := d.engine.Take(ctx, taskID, d.actor)
	if err != nil {
		d.printf("take failed: id=%s: %v\n", taskID, err)

		return
	}

	outputs, err := collect(view.Renderers)
	if err != nil {
		d.printf("render failed: id=%s: %v\n", taskID, err)

		return
	}

	if err := d.engine.Complete(ctx, taskID, d.actor, outputs); err != nil {
		d.printf("complete failed: id=%s: %v\n", taskID, err)

		return
	}

	d.printf("task completed: id=%s\n", taskID)
}

// printf writes a progress line, ignoring the writer error (progress is
// best-effort).
func (d *Driver) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(d.w, format, a...)
}

// collect renders the first renderer to gather the task's outputs. A task with no
// renderer completes with no outputs (valid only if it declares no required
// output).
func collect(renderers []hi.Renderer) ([]data.Data, error) {
	if len(renderers) == 0 {
		return nil, nil
	}

	return renderers[0].Render(nil)
}

var _ interactor.TaskDistributor = (*Driver)(nil)
