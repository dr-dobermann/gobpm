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
  versioning:
    register two versions of one key ("greeter"), then address them by
    latest / version-number / handle, and watch promote-on-removal.

`)
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("versioning-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	if err := demonstrateVersioning(ctx, engine); err != nil {
		return err
	}

	fmt.Println("\n✓ versioning example completed")

	return nil
}
