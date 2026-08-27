// Command event-data demonstrates event data attachment (ADR-040 v.2 §2.7,
// SRD-094): a message START EVENT's output association fills a declared
// process input from the message payload, the same payload lands in a data
// object, and a message END EVENT's input association sources a declared
// process output — the standard's Start/End special case (§10.4.2), which
// lets the message route reach the same contract a Call Activity binds. The
// host publishes "order placed" and receives "quote ready" carrying the
// total the process computed. See README.md.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  event-data:
    quote:  start[order placed] → price → end[quote ready]
            start ──▶ input "order" (and data object "received")
            output "total" ──▶ end
`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	broker := membroker.New()

	engine, err := thresher.New("event-data-engine",
		thresher.WithMessageBroker(broker),
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	quote, err := buildQuote()
	if err != nil {
		return fmt.Errorf("build quote: %w", err)
	}

	if _, err = engine.RegisterProcess(quote); err != nil {
		return fmt.Errorf("register quote: %w", err)
	}

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	return roundTrip(ctx, broker)
}
