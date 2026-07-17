package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// rootDatum builds a Ready root datum named name carrying an int value — an
// input handed across the call boundary (SRD-050).
func rootDatum(t *testing.T, name string, v int) data.Data {
	t.Helper()

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v),
				foundation.WithID(name)),
			data.ReadyDataState))
}

// startInvoker spins a Run-ing thresher and returns it with a cleanup.
func startInvoker(t *testing.T, name string) (*thresher.Thresher, context.Context) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	th, err := thresher.New(name)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		require.NoError(t, th.Shutdown(context.Background()))
		cancel()
	})

	require.NoError(t, th.Run(ctx))

	return th, ctx
}

// waitChild blocks until the child reaches a terminal state (or a short
// deadline), asserting it completed without a fault.
func waitChild(t *testing.T, child exec.ChildProcess) {
	t.Helper()

	select {
	case <-child.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("child instance did not complete in time")
	}

	if err := child.Failed(); err != nil {
		t.Fatalf("child instance faulted: %v", err)
	}
}

// TestInvokeProcessLatestAndPinned (SRD-050 §6, M2): version 0 resolves the
// newest registration; a pin binds an exact version and survives a later
// registration; a missing key/version and the bad-argument shapes are classified
// errors.
func TestInvokeProcessLatestAndPinned(t *testing.T) {
	th, ctx := startInvoker(t, "invoke-resolve")

	// Two versions of the same key: re-registering the SAME definition object
	// mints a new version under its key (proc.ID()) — the second supersedes → v2.
	callee := linearProcess(t, "callee", 0)
	_, err := th.RegisterProcess(callee)
	require.NoError(t, err)
	_, err = th.RegisterProcess(callee)
	require.NoError(t, err)

	call := func(v int) exec.ProcessCall {
		return exec.ProcessCall{
			Key: callee.ID(), Version: v,
			ParentInstanceID: "parent-1", CallNodeID: "call-1",
		}
	}

	t.Run("latest binds the newest version", func(t *testing.T) {
		child, err := th.InvokeProcess(ctx, call(0))
		require.NoError(t, err)
		require.Equal(t, 2, child.Version(), "latest-at-launch = v2")
		waitChild(t, child)
	})

	t.Run("pin binds the exact version", func(t *testing.T) {
		child, err := th.InvokeProcess(ctx, call(1))
		require.NoError(t, err)
		require.Equal(t, 1, child.Version())
		waitChild(t, child)
	})

	t.Run("a pin survives a later registration", func(t *testing.T) {
		_, err := th.RegisterProcess(callee) // v3 of the same key
		require.NoError(t, err)

		child, err := th.InvokeProcess(ctx, call(1))
		require.NoError(t, err)
		require.Equal(t, 1, child.Version(), "the pin ignores the newer v3")
		waitChild(t, child)
	})

	t.Run("missing key is a classified error", func(t *testing.T) {
		_, err := th.InvokeProcess(ctx, exec.ProcessCall{
			Key: "absent", ParentInstanceID: "p", CallNodeID: "c"})
		require.Error(t, err)
	})

	t.Run("missing version is a classified error", func(t *testing.T) {
		_, err := th.InvokeProcess(ctx, call(99))
		require.Error(t, err)
	})

	t.Run("bad-argument shapes are rejected", func(t *testing.T) {
		_, err := th.InvokeProcess(ctx, exec.ProcessCall{
			Key: "", ParentInstanceID: "p", CallNodeID: "c"})
		require.Error(t, err, "empty key")

		_, err = th.InvokeProcess(ctx, exec.ProcessCall{
			Key: callee.ID(), Version: -1,
			ParentInstanceID: "p", CallNodeID: "c"})
		require.Error(t, err, "negative version")

		_, err = th.InvokeProcess(ctx, exec.ProcessCall{Key: callee.ID()})
		require.Error(t, err, "missing linkage")
	})

	t.Run("a not-started engine rejects the call", func(t *testing.T) {
		require.NoError(t, data.CreateDefaultStates())
		idle, err := thresher.New("idle-engine")
		require.NoError(t, err)

		_, err = idle.InvokeProcess(ctx, exec.ProcessCall{
			Key: "x", ParentInstanceID: "p", CallNodeID: "c"})
		require.Error(t, err, "the engine isn't Started")
	})
}

// TestInvokeProcessInputs (SRD-050 §6, M2): the call's inputs are seeded into the
// child's root scope — the child reads them by name after launch.
func TestInvokeProcessInputs(t *testing.T) {
	th, ctx := startInvoker(t, "invoke-inputs")

	callee := linearProcess(t, "in-callee", 0)
	_, err := th.RegisterProcess(callee)
	require.NoError(t, err)

	child, err := th.InvokeProcess(ctx, exec.ProcessCall{
		Key: callee.ID(), ParentInstanceID: "parent-1", CallNodeID: "call-1",
		Inputs: []data.Data{rootDatum(t, "order", 42)},
	})
	require.NoError(t, err)
	waitChild(t, child)

	got, err := child.Outputs([]string{"order"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 42, got[0].Value().Get(ctx),
		"the seeded input lands in the child's root scope")
}

// TestChildOutputsReader (SRD-050 §6, M2): Outputs reads the child's declared
// return values by name after completion; an undeclared name is a classified
// error (the call contract broken).
func TestChildOutputsReader(t *testing.T) {
	th, ctx := startInvoker(t, "invoke-outputs")

	callee := linearProcess(t, "out-callee", 0)
	_, err := th.RegisterProcess(callee)
	require.NoError(t, err)

	child, err := th.InvokeProcess(ctx, exec.ProcessCall{
		Key: callee.ID(), ParentInstanceID: "parent-1", CallNodeID: "call-1",
		Inputs: []data.Data{rootDatum(t, "a", 1), rootDatum(t, "b", 2)},
	})
	require.NoError(t, err)
	waitChild(t, child)

	got, err := child.Outputs([]string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, got, 2)

	_, err = child.Outputs([]string{"missing"})
	require.Error(t, err, "an undeclared output breaks the call contract")

	// Terminate is idempotent on an already-terminal child (the cascade M3 uses).
	require.NotPanics(t, child.Terminate)
}

// TestChildFailedReportsFault (SRD-050 §6, M2): a child that faults ends
// abnormally — Failed reports the terminal error, the signal M3 turns into the
// caller-track fault.
func TestChildFailedReportsFault(t *testing.T) {
	th, ctx := startInvoker(t, "invoke-fault")

	// total=50 makes both conditional flows false with no default → the child
	// faults on the zero-selected activity outgoing (SRD-046 FR-4).
	callee := flowsFaultProcess(t, "fault-callee", 50)
	_, err := th.RegisterProcess(callee)
	require.NoError(t, err)

	child, err := th.InvokeProcess(ctx, exec.ProcessCall{
		Key: callee.ID(), ParentInstanceID: "parent-1", CallNodeID: "call-1"})
	require.NoError(t, err)

	select {
	case <-child.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("faulting child did not terminate in time")
	}

	require.Error(t, child.Failed(), "the child ended abnormally")
}

// TestChildLinkageFacts (SRD-050 §6, M2): every fact the child emits carries the
// caller's linkage (parent_instance_id + call_activity_node_id), stitching the
// trace across the reuse boundary.
func TestChildLinkageFacts(t *testing.T) {
	th, ctx := startInvoker(t, "invoke-linkage")

	c := &collector{}
	sub := th.Observe(c)

	callee := linearProcess(t, "link-callee", 0)
	_, err := th.RegisterProcess(callee)
	require.NoError(t, err)

	child, err := th.InvokeProcess(ctx, exec.ProcessCall{
		Key: callee.ID(), ParentInstanceID: "parent-7", CallNodeID: "call-9",
	})
	require.NoError(t, err)
	childID := child.ID()
	waitChild(t, child)

	sub.Cancel() // drain the buffered facts

	require.True(t, c.hasChildLinkage(childID, "parent-7", "call-9"),
		"the child's facts carry the caller's linkage")
}

// hasChildLinkage reports whether some fact of the child instance carries the
// expected parent-instance and call-node linkage.
func (c *collector) hasChildLinkage(childID, parentID, callNodeID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range c.events {
		if e.Details[observability.AttrInstanceID] != childID {
			continue
		}

		if e.Details[observability.AttrParentInstanceID] == parentID &&
			e.Details[observability.AttrCallActivityNodeID] == callNodeID {
			return true
		}
	}

	return false
}
