// Command process-data demonstrates the process data model (ADR-010 /
// SRD-007): a process property lives in the instance's container scope, two
// parallel branches read it through their own execution frames, each branch
// produces its result through its frame, and the results reach the bound
// DataObjects when the frames commit.
//
//	start ─> split ─┬─> greet-a ─> end-a       (result-a DataObject)
//	                └─> greet-b ─> end-b       (result-b DataObject)
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("create default states: %w", err)
	}

	engine, err := thresher.New("process-data-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	// the process property lands in the instance's root container scope at
	// start; branch tasks resolve it through their frames' container walk.
	proc, err := process.New("data-demo",
		data.WithProperties(
			data.MustProperty("user_name",
				data.MustItemDefinition(
					values.NewVariable("dr.Dobermann"),
					foundation.WithID("user_name")),
				data.ReadyDataState)))
	if err != nil {
		return fmt.Errorf("create process: %w", err)
	}

	done := make(chan string, 2)

	start, err := events.NewStartEvent("start")
	if err != nil {
		return fmt.Errorf("create start: %w", err)
	}

	split, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	if err != nil {
		return fmt.Errorf("create split: %w", err)
	}

	greetA, resultA, err := newGreeter("greet-a", "res-a", "Hello", done)
	if err != nil {
		return err
	}

	greetB, resultB, err := newGreeter("greet-b", "res-b", "Welcome", done)
	if err != nil {
		return err
	}

	endA, err := events.NewEndEvent("end-a")
	if err != nil {
		return fmt.Errorf("create end-a: %w", err)
	}

	endB, err := events.NewEndEvent("end-b")
	if err != nil {
		return fmt.Errorf("create end-b: %w", err)
	}

	for _, e := range []flow.Element{start, split, greetA, greetB, endA, endB} {
		if err := proc.Add(e); err != nil {
			return fmt.Errorf("add element: %w", err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, split},
		{split, greetA}, {split, greetB},
		{greetA, endA}, {greetB, endB},
	} {
		if err := link(l[0], l[1]); err != nil {
			return err
		}
	}

	if err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	if err := engine.StartProcess(proc.ID()); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	ran := map[string]bool{}
	for len(ran) < 2 {
		select {
		case name := <-done:
			ran[name] = true
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for branches (ran: %v)", ran)
		}
	}

	// brief grace for the producer stages: outputs flow to the DataObjects
	// and the frames commit.
	time.Sleep(200 * time.Millisecond)

	bg := context.Background()

	for _, res := range []struct {
		do   *dataobjects.DataObject
		want string
	}{
		{resultA, "Hello, dr.Dobermann!"},
		{resultB, "Welcome, dr.Dobermann!"},
	} {
		got, ok := res.do.Subject().Structure().Get(bg).(string)
		if !ok || got != res.want {
			return fmt.Errorf("data object %q: want %q, got %v",
				res.do.Name(), res.want, got)
		}

		fmt.Printf("  ✓ %s = %q\n", res.do.Name(), got)
	}

	fmt.Println("✓ data-demo completed: the property fed both branches " +
		"through their frames; each result reached its DataObject")

	return nil
}

// newGreeter builds a ServiceTask whose operation reads the user_name data
// through the execution environment and produces a greeting, plus the
// DataObject its output association feeds.
func newGreeter(
	name, resID, greeting string,
	done chan<- string,
) (*activities.ServiceTask, *dataobjects.DataObject, error) {
	in := bpmncommon.MustMessage(name+"-in",
		data.MustItemDefinition(
			values.NewVariable(""),
			foundation.WithID("user_name")))

	out := bpmncommon.MustMessage(name+"-out",
		data.MustItemDefinition(
			values.NewVariable(""),
			foundation.WithID(resID)))

	impl, err := gooper.New(
		func(ctx context.Context, d *data.ItemDefinition) (*data.ItemDefinition, error) {
			user, _ := d.Structure().Get(ctx).(string)
			res := fmt.Sprintf("%s, %s!", greeting, user)

			fmt.Printf("  ▶ %s produced %q\n", name, res)
			done <- name

			return data.MustItemDefinition(
					values.NewVariable(res),
					foundation.WithID(resID)),
				nil
		})
	if err != nil {
		return nil, nil, fmt.Errorf("create %s implementor: %w", name, err)
	}

	op, err := service.NewOperation(name+"-op", in, out, impl)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	if err != nil {
		return nil, nil, fmt.Errorf("create %s task: %w", name, err)
	}

	// declare the task output the operation result fills (the producer
	// stage copies the frame put into this output's per-execution instance).
	outSet := data.MustSet(name + "-outs")
	if err := st.IoSpec.AddSet(outSet, data.Output); err != nil {
		return nil, nil, fmt.Errorf("add %s output set: %w", name, err)
	}

	outParam := data.MustParameter(name+" result",
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(""),
				foundation.WithID(resID)),
			data.UnavailableDataState))

	if err := st.IoSpec.AddParameter(outParam, data.Output); err != nil {
		return nil, nil, fmt.Errorf("add %s output: %w", name, err)
	}

	if err := outSet.AddParameter(outParam, data.DefaultSet); err != nil {
		return nil, nil, fmt.Errorf("bind %s output set: %w", name, err)
	}

	// the DataObject the branch result lands in via the output association.
	resDO, err := dataobjects.New(name+"-result",
		data.MustItemDefinition(
			values.NewVariable(""),
			foundation.WithID(resID)),
		nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s result object: %w", name, err)
	}

	if err := resDO.AssociateSource(st, []string{resID}, nil); err != nil {
		return nil, nil, fmt.Errorf("bind %s result object: %w", name, err)
	}

	return st, resDO, nil
}

// link connects two flow elements with a sequence flow.
func link(src, trg flow.Element) error {
	s, ok := src.(flow.SequenceSource)
	if !ok {
		return fmt.Errorf("%q is not a sequence source", src.Name())
	}

	t, ok := trg.(flow.SequenceTarget)
	if !ok {
		return fmt.Errorf("%q is not a sequence target", trg.Name())
	}

	if _, err := flow.Link(s, t); err != nil {
		return fmt.Errorf("link: %w", err)
	}

	return nil
}
