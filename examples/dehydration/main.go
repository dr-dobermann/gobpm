// Dehydration: a long wait costs no goroutines.
//
// An instance whose every live track is parked on a wait that is both
// DEHYDRATABLE (the element allows it) and HELD (the engine can wake it)
// releases all of its goroutines — loop included — and leaves. Its checkpoint
// is the wake source. This example walks every holder kind and, for timers,
// both sides of the threshold.
//
// It drives a CONTROLLED clock so a two-hour wait demonstrates in
// milliseconds. In production you use the default wall clock and change
// nothing else.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// epoch anchors the controlled clock so every deadline below is "epoch + d".
var epoch = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dehydration example failed:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := data.CreateDefaultStates(); err != nil {
		return err
	}

	clk := clocktest.New(epoch)
	broker := membroker.New()
	inbox := newInbox()
	watch := newResidency()

	// WithRepository is the master switch: it arms checkpointing, restart
	// recovery AND dehydration. Without it nothing ever releases.
	eng, err := thresher.New("dehydration-demo",
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(memrepo.New()),
		thresher.WithMessageBroker(broker),
		thresher.WithTaskDistributor(inbox),
		thresher.WithClock(clk))
	if err != nil {
		return err
	}

	sub := eng.Observe(watch)
	defer sub.Cancel()

	demo := &demo{eng: eng, clk: clk, broker: broker, inbox: inbox, watch: watch}

	if err := demo.registerAll(); err != nil {
		return err
	}

	if err := eng.Run(ctx); err != nil {
		return err
	}

	for _, c := range demo.cases() {
		fmt.Printf("\n%s\n   %s\n", c.title, c.note)

		if err := c.run(); err != nil {
			return fmt.Errorf("%s: %w", c.title, err)
		}
	}

	fmt.Printf("\n%d of the 6 instances released their goroutines and came "+
		"back; the near-deadline timer stayed resident on purpose\n",
		watch.dehydrations())

	return eng.Shutdown(ctx)
}
