// Command data-store demonstrates the engine-global Data Store (BPMN §10.4.1,
// ADR-030 / SRD-064): a value one process instance writes into a
// DataStoreReference outlives that instance and is read by a *separate*
// instance through a reference to the same store — the store is shared across
// instances within the running engine, wired via thresher.WithDataStore.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "data-store example failed:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := data.CreateDefaultStates(); err != nil {
		return err
	}

	seen := make(chan int, 1)

	writer, err := buildWriter()
	if err != nil {
		return err
	}

	reader, err := buildReader(seen)
	if err != nil {
		return err
	}

	// one engine, one in-memory store registered under "shared".
	eng, err := thresher.New("data-store-demo",
		thresher.WithDataStore(storeRef, memstore.New()))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := eng.Run(ctx); err != nil {
		return err
	}

	if _, err := eng.RegisterProcess(writer); err != nil {
		return err
	}

	if _, err := eng.RegisterProcess(reader); err != nil {
		return err
	}

	// the writer instance stores 42 into the shared store, then completes.
	if err := runToDone(ctx, eng, writer.ID()); err != nil {
		return err
	}

	fmt.Println(`✓ writer instance stored 42 in DataStore "shared" key "counter"`)

	// a separate reader instance reads it back — the value crossed instances.
	if err := runToDone(ctx, eng, reader.ID()); err != nil {
		return err
	}

	got := <-seen
	if got != 42 {
		return fmt.Errorf("reader read %d, want 42", got)
	}

	fmt.Printf("✓ reader instance read %d from the shared DataStore\n", got)
	fmt.Println("✓ data-store demo: the value outlived the writer instance and " +
		"crossed into the reader through the engine-global store")

	return nil
}

func runToDone(
	ctx context.Context, eng *thresher.Thresher, procID string,
) error {
	h, err := eng.StartLatest(procID)
	if err != nil {
		return err
	}

	_, err = h.WaitCompletion(ctx)

	return err
}
