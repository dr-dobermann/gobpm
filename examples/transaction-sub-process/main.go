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

func run() error {
	fmt.Print(`
  transaction-sub-process (a booking Transaction that cancels):
    start → (booking) → end
              ⚡ cancel-bnd → notify-customer
    booking = reserve-seat → charge-card → cancel-booking (Cancel End)
                ╳ release-seat  ╳ refund-card
    the Cancel End aborts: refund-card runs BEFORE release-seat, then
    control exits the Cancel boundary to notify-customer

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("transaction-sub-process-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("\n✓ transaction-sub-process completed (%s): the Cancel End "+
		"compensated the booking in reverse order and control left through the "+
		"Cancel boundary\n", state)

	return nil
}
