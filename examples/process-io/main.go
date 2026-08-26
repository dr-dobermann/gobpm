// Command process-io demonstrates the Process I/O contract (ADR-040,
// SRD-093): a process DECLARES its inputs and outputs, and the engine holds
// both boundaries to that declaration. A host launches "quote" with a value
// for the required "amount", the Call Activity inside it reaches the child
// "rate" through the child's own contract, the stamp task publishes an
// engine runtime variable under a declared output, and the host reads the
// collected result from the handle. Two more launches are refused at the
// boundary — before anything runs: one binds nothing to the required input,
// one delivers a datum the contract does not declare. See README.md.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// The child adds 20% tax; the input and every assertion read these.
const (
	amount    = 100
	wantTotal = 120
)

func run() error {
	fmt.Print(`
  process-io:
    quote: start → price[calls "rate"] → stamp → end
           in: amount (required), currency (optional)
           out: total (required), started_at (optional)
    rate:  start → tax → end
           in: amount   out: total

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("process-io-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	got := newSeen()

	quote, err := register(engine, got)
	if err != nil {
		return err
	}

	if err := launchBound(ctx, engine, quote, got); err != nil {
		return err
	}

	return launchRefused(engine, quote)
}

// register builds and registers the child and the quote process.
func register(engine *thresher.Thresher, got *seen) (string, error) {
	rate, err := buildRate()
	if err != nil {
		return "", fmt.Errorf("build rate: %w", err)
	}

	quote, err := buildQuote(got)
	if err != nil {
		return "", fmt.Errorf("build quote: %w", err)
	}

	if _, err = engine.RegisterProcess(rate); err != nil {
		return "", fmt.Errorf("register rate: %w", err)
	}

	if _, err = engine.RegisterProcess(quote); err != nil {
		return "", fmt.Errorf("register quote: %w", err)
	}

	return quote.ID(), nil
}
