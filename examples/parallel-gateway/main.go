// Command parallel-gateway demonstrates the BPMN Parallel (AND) gateway
// (SRD-005): a diverging gateway forks every outgoing branch, the branches run
// concurrently, and a converging gateway synchronizes them — the process
// continues only after every branch has arrived.
//
//	start ─> split ─┬─> worker-a ─┬─> join ─> end
//	                └─> worker-b ─┘
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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
	fmt.Print(`
  parallel-gateway:
    start ─> split ─┬─> worker-a ─┬─> join ─> end
                    └─> worker-b ─┘

`)
	engine, err := thresher.New("parallel-gateway-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := process.New("parallel-demo")
	if err != nil {
		return fmt.Errorf("create process: %w", err)
	}

	// done reports which worker branch executed; main waits for both, proving
	// the diverging gateway forked and ran the branches concurrently.
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

	workerA, err := newWorker("worker-a", done)
	if err != nil {
		return err
	}

	workerB, err := newWorker("worker-b", done)
	if err != nil {
		return err
	}

	join, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Converging))
	if err != nil {
		return fmt.Errorf("create join: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, split, workerA, workerB, join, end} {
		if err := proc.Add(e); err != nil {
			return fmt.Errorf("add element: %w", err)
		}
	}

	// start ─> split ─┬─> worker-a ─┬─> join ─> end
	//                 └─> worker-b ─┘
	for _, l := range [][2]flow.Element{
		{start, split},
		{split, workerA}, {split, workerB},
		{workerA, join}, {workerB, join},
		{join, end},
	} {
		if err := link(l[0], l[1]); err != nil {
			return err
		}
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	if _, err := engine.StartProcess(proc.ID()); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	// wait for both branches; the converging gateway then synchronizes them and
	// the surviving token reaches End.
	ran := map[string]bool{}
	for len(ran) < 2 {
		select {
		case name := <-done:
			ran[name] = true
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for parallel branches (ran: %v)", ran)
		}
	}

	// brief grace for the join to synchronize and the survivor to reach End.
	time.Sleep(100 * time.Millisecond)

	fmt.Println("✓ parallel-demo completed: split forked both branches, " +
		"join synchronized, one token reached End")

	return nil
}

// newWorker builds a ServiceTask whose operation is a Go functor: it prints its
// execution and signals done, so the example shows real per-branch work rather
// than a silent no-op.
func newWorker(name string, done chan<- string) (*activities.ServiceTask, error) {
	op, err := gooper.New(
		name+"-op",
		func(_ context.Context, _ service.DataReader, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Printf("  ▶ %s executed\n", name)
			done <- name

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create %s task: %w", name, err)
	}

	return st, nil
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
