package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// demonstrateVersioning walks the ADR-019 versioning lifecycle on one key:
// register two versions, address them by latest / number / handle, list them,
// then unregister the latest and watch the previous version get promoted back
// to "latest". Each greeter run prints its own label, so the console proves
// which version actually executed.
func demonstrateVersioning(
	ctx context.Context,
	engine *thresher.Thresher,
) error {
	v1, err := register(engine, "v1")
	if err != nil {
		return err
	}

	v2, err := register(engine, "v2")
	if err != nil {
		return err
	}

	// Latest is now v2 — StartLatest resolves the highest version number.
	h, err := engine.StartLatest(processKey)
	if err := startAndWait(ctx, "StartLatest        → expects v2", h, err); err != nil {
		return err
	}

	// Pin an older version explicitly, by number, without holding its handle.
	h, err = engine.StartVersion(processKey, 1)
	if err := startAndWait(ctx, "StartVersion(key,1)→ expects v1", h, err); err != nil {
		return err
	}

	// Same version, addressed by the registration handle returned earlier.
	h, err = engine.StartProcess(v1)
	if err := startAndWait(ctx, "StartProcess(v1)   → expects v1", h, err); err != nil {
		return err
	}

	fmt.Printf("  registered versions of %q: %s\n",
		processKey, versionList(engine))

	// Unregister the latest (v2): promote-on-removal makes v1 the latest again,
	// symmetric with how registering v2 superseded v1.
	if err := engine.UnregisterVersion(v2); err != nil {
		return fmt.Errorf("unregister v2: %w", err)
	}

	fmt.Printf("  after UnregisterVersion(v2), versions: %s\n",
		versionList(engine))

	h, err = engine.StartLatest(processKey)
	if err := startAndWait(ctx, "StartLatest        → expects v1 (promoted)", h, err); err != nil {
		return err
	}

	return nil
}

// register builds a fresh greeter carrying the given release label under the
// shared key and registers it, reporting the version the engine assigned.
func register(
	engine *thresher.Thresher,
	label string,
) (*thresher.ProcessRegistration, error) {
	proc, err := buildGreeter(label)
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", label, err)
	}

	reg, err := engine.RegisterProcess(proc)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", label, err)
	}

	fmt.Printf("  registered %s → key=%q version=%d\n",
		label, reg.Key(), reg.Version())

	return reg, nil
}

// startAndWait consumes a Start* call's (handle, error) result, runs the
// instance to completion, and prints the labelled outcome. Threading the Start*
// result straight in keeps each call site a single readable line.
func startAndWait(
	ctx context.Context,
	label string,
	h *thresher.InstanceHandle,
	err error,
) error {
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("%s: wait completion: %w", label, err)
	}

	fmt.Printf("  %s  [instance %s]\n", label, state)

	return nil
}

// versionList renders the live version numbers registered for the key, e.g.
// "[1 2]", so the console shows the registry shrink after an unregister.
func versionList(engine *thresher.Thresher) string {
	regs := engine.Registrations(processKey)

	nums := make([]int, 0, len(regs))
	for _, r := range regs {
		nums = append(nums, r.Version())
	}

	return fmt.Sprintf("%v", nums)
}
