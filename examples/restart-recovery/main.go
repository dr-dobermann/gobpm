// Command restart-recovery demonstrates ADR-033/SRD-070 instance
// checkpoints and restart recovery: engine-1 parks an instance on a
// timer and "crashes" (it is simply abandoned — no graceful terminal
// write); engine-2, sharing the SAME repository, claims the expired
// lease, restores the instance at the RECORDED deadline and finishes
// the process. One OS process, two engines, one store — the same trace
// a real restart follows.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Print(`
  restart-recovery (one store, two engines):
    engine-1: start → park on timer → checkpoint → "crash" (abandoned)
    engine-2: claim expired lease → restore at the RECORDED deadline
              → timer fires → complete

`)

	repo := memrepo.New() // the shared state of record

	deadline := time.Now().Add(2 * time.Second)

	// ---- engine-1: run to the park, then abandon it ----------------
	e1, err := thresher.New("engine-1",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithLeaseTTL(500*time.Millisecond))
	if err != nil {
		return fmt.Errorf("engine-1: %w", err)
	}

	proc1, err := buildProcess("engine-1 (zombie)", deadline)
	if err != nil {
		return err
	}

	if _, err = e1.RegisterProcess(proc1); err != nil {
		return fmt.Errorf("engine-1 register: %w", err)
	}

	ctx1, crash := context.WithCancel(context.Background())
	defer crash()

	if err = e1.Run(ctx1); err != nil {
		return fmt.Errorf("engine-1 run: %w", err)
	}

	h, err := e1.StartLatest(proc1.ID())
	if err != nil {
		return fmt.Errorf("engine-1 start: %w", err)
	}

	fmt.Printf("  engine-1: instance %s parked on the timer, "+
		"checkpointed\n", h.ID())

	time.Sleep(700 * time.Millisecond) // the lease expires; nobody renews

	fmt.Print("  engine-1: 💥 abandoned (no graceful shutdown — the " +
		"record stays Active)\n\n")

	// ---- engine-2: same store, same registered process -------------
	e2, err := thresher.New("engine-2",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo))
	if err != nil {
		return fmt.Errorf("engine-2: %w", err)
	}

	proc2, err := buildProcess("engine-2 (recovering)", deadline)
	if err != nil {
		return err
	}

	if _, err := e2.RegisterProcess(proc2); err != nil {
		return fmt.Errorf("engine-2 register: %w", err)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// Run recovers the claimable in-flight instances from the store.
	if err := e2.Run(ctx2); err != nil {
		return fmt.Errorf("engine-2 run: %w", err)
	}

	fmt.Println("  engine-2: recovered the instance from the checkpoint")

	// wait for the recovered instance to complete in the store.
	deadlineCtx, dc := context.WithTimeout(context.Background(),
		10*time.Second)
	defer dc()

	for {
		rec, ok, err := repo.Load(deadlineCtx, h.ID())
		if err != nil {
			return err
		}

		if ok && rec.Status.IsTerminal() {
			fmt.Printf("\n✓ restart-recovery completed: the record is "+
				"terminal and owned by %q.\n  (Both engines printed the "+
				"effect — effects are at-least-once; only the recovering "+
				"engine's STATE survived, the zombie's saves were "+
				"CAS-fenced.)\n", rec.Lease.Owner)

			return nil
		}

		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("the recovered instance never completed")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
