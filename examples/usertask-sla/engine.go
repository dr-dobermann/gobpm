package main

import (
	"context"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// runProcess starts the engine, registers p and waits for its single instance
// to settle, returning the terminal state.
//
// It is a function rather than a stretch of run() so that each step's error
// lives in one short scope. Threading a single err through the whole run made
// every `if err := …` after the first one a shadow of it, and rewriting those
// as assignments only traded the complaint for the opposite one.
func runProcess(
	ctx context.Context,
	th *thresher.Thresher,
	p *process.Process,
) (thresher.InstanceState, error) {
	if err := th.Run(ctx); err != nil {
		return "", err
	}

	if _, err := th.RegisterProcess(p); err != nil {
		return "", err
	}

	h, err := th.StartLatest(p.ID())
	if err != nil {
		return "", err
	}

	wctx, wc := context.WithTimeout(context.Background(), 30*time.Second)
	defer wc()

	return h.WaitCompletion(wctx)
}
