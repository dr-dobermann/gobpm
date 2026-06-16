// Command inter-instance-correlation demonstrates event-triggered instantiation
// with key-based message correlation (ADR-015 / ADR-016 v.1, SRD-015):
//
//	process A (source)                 process B (handler, auto-instantiated)
//	begin ─▶ send ORD-1 ─▶ send ORD-2   (order placed) ─▶ report ─▶ end
//	          │             │                  ▲
//	          └──────"order placed"────────────┘  routed/instantiated by key
//
// Process A is started explicitly and publishes two "order placed" messages
// (ORD-1, ORD-2). Process B has a correlation-keyed message start event and is
// **not** started explicitly — the engine instantiates one B per distinct order
// key when the message arrives (no instance exists before its trigger). Two
// orders ⇒ two handler instances, disambiguated by the payload-derived key.
//
// The instantiation decision (phase-2b) is driven by B's consumer-side key
// derivation from the payload; A's SendTask also stamps the key onto the
// envelope (producer side), which keyed in-instance receivers will use in a
// later phase (ADR-016 §2.4, phase-2c).
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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

	engine, err := thresher.New("correlation-demo-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	// one order id per born handler instance.
	done := make(chan string, len(orders))

	consumer, err := newConsumer(done)
	if err != nil {
		return fmt.Errorf("build consumer: %w", err)
	}

	producer, err := newProducer()
	if err != nil {
		return fmt.Errorf("build producer: %w", err)
	}

	// the handler is registered for auto-instantiation (a matching message
	// spawns it); the source is started explicitly below.
	if err := engine.RegisterProcess(consumer); err != nil {
		return fmt.Errorf("register consumer: %w", err)
	}

	if err := engine.RegisterProcess(producer); err != nil {
		return fmt.Errorf("register producer: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	if err := engine.StartProcess(producer.ID()); err != nil {
		return fmt.Errorf("start producer: %w", err)
	}

	// collect one confirmation per distinct order — proof that each order
	// instantiated its own handler instance.
	handled := map[string]bool{}
	for len(handled) < len(orders) {
		select {
		case id := <-done:
			if !handled[id] {
				handled[id] = true
				fmt.Printf("  ✓ order %q instantiated its own handler instance\n",
					id)
			}
		case <-ctx.Done():
			return fmt.Errorf("timed out: %d/%d orders handled",
				len(handled), len(orders))
		}
	}

	fmt.Printf("✓ inter-instance correlation: %d orders ⇒ %d handler "+
		"instances, routed by key\n", len(orders), len(handled))

	return nil
}

// link wires src -> trg with a sequence flow.
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
