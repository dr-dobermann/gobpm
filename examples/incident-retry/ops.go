package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// start brings the engine up with a repository — incidents are durable state,
// and an operator op on a parked instance rebuilds it from its checkpoint —
// registers the process and starts one instance.
func start() (*thresher.InstanceHandle, func(), error) {
	p, err := buildProcess()
	if err != nil {
		return nil, nil, err
	}

	th, err := thresher.New("incident-retry",
		thresher.WithoutBanner(),
		thresher.WithRepository(memrepo.New()))
	if err != nil {
		return nil, nil, fmt.Errorf("engine: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	if err := th.Run(ctx); err != nil {
		cancel()

		return nil, nil, fmt.Errorf("engine run: %w", err)
	}

	if _, err := th.RegisterProcess(p); err != nil {
		cancel()

		return nil, nil, fmt.Errorf("register: %w", err)
	}

	h, err := th.StartLatest(p.ID())
	if err != nil {
		cancel()

		return nil, nil, fmt.Errorf("start: %w", err)
	}

	return h, cancel, nil
}

// operate is the operator's session: wait for the incident to exhaust its
// policy, inspect it, retry it, and wait for the completion.
func operate(
	h *thresher.InstanceHandle,
) (thresher.InstanceState, thresher.IncidentView, error) {
	// The policy's automatic retry runs first; the incident is "ours" once
	// its second failure leaves it open with no schedule.
	view, err := waitOpenIncident(h)
	if err != nil {
		return "", view, err
	}

	fmt.Println("⚠ incident waiting for an operator:")
	fmt.Printf("  node:     %s\n", view.NodeName)
	fmt.Printf("  attempts: %d (the raise + the policy's retry)\n",
		view.Attempts)
	fmt.Printf("  cause:    %.60s…\n",
		strings.ReplaceAll(view.Cause, "\n", " · "))
	fmt.Printf("  snapshot: %s\n", string(view.Data))
	fmt.Println("→ operator: retry the incident")

	if err := h.RetryIncident(context.Background(), view.ID); err != nil {
		return "", view, fmt.Errorf("retry: %w", err)
	}

	wctx, wcancel := context.WithTimeout(context.Background(),
		10*time.Second)
	defer wcancel()

	state, err := h.WaitCompletion(wctx)
	if err != nil {
		return state, view, fmt.Errorf("completion: %w", err)
	}

	return state, h.Incidents()[0], nil
}

// waitOpenIncident polls for the incident that has exhausted its automatic
// retries: open, unscheduled, with both attempts recorded.
func waitOpenIncident(
	h *thresher.InstanceHandle,
) (thresher.IncidentView, error) {
	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		for _, v := range h.Incidents() {
			if v.State == "open" && v.Attempts == 2 && v.RetryAt.IsZero() {
				return v, nil
			}
		}

		time.Sleep(10 * time.Millisecond)
	}

	return thresher.IncidentView{},
		fmt.Errorf("no operator-waiting incident within the deadline")
}
