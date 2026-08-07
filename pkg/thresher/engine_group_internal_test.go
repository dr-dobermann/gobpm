package thresher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// SRD-078 T-6 — the internal halves: identity resolution and the
// ensureGroup registry-failure paths.

func TestResolveIdentityDefaults(t *testing.T) {
	cfg := defaultConfig()

	id, group, err := resolveIdentity("  ", &cfg)
	require.NoError(t, err)
	require.Equal(t, defaultThresherID, id,
		"a blank id must fall back to the default engine id")
	require.Equal(t, id, group,
		"the solo default group is the resolved engine id (§4.7)")
}

// registryFaults injects failures into the group-registry methods.
type registryFaults struct {
	*memrepo.Repo
	registerErr error
	existsErr   error
}

func (rf *registryFaults) RegisterGroup(ctx context.Context, g string) error {
	if rf.registerErr != nil {
		return rf.registerErr
	}

	return rf.Repo.RegisterGroup(ctx, g)
}

func (rf *registryFaults) GroupExists(
	ctx context.Context, g string,
) (bool, error) {
	if rf.existsErr != nil {
		return false, rf.existsErr
	}

	return rf.Repo.GroupExists(ctx, g)
}

func TestEnsureGroupRegistryFaults(t *testing.T) {
	ctx := context.Background()

	t.Run("a failing RegisterGroup fails the establish", func(t *testing.T) {
		th, err := New("eg-faults-1",
			WithoutBanner(), WithoutStartupConfig(),
			WithRepository(&registryFaults{
				Repo:        memrepo.New(),
				registerErr: context.DeadlineExceeded,
			}))
		require.NoError(t, err)

		err = th.ensureGroup(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't register engine group")
	})

	t.Run("a failing GroupExists fails the assert", func(t *testing.T) {
		th, err := New("eg-faults-2",
			WithoutBanner(), WithoutStartupConfig(),
			WithRepository(&registryFaults{
				Repo:      memrepo.New(),
				existsErr: context.DeadlineExceeded,
			}),
			WithExistingEngineGroup("cluster-x"))
		require.NoError(t, err)

		err = th.ensureGroup(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't check engine group")
	})
}

// TestHandleLinkageBeforeAdopt: a handle not yet pointing at an
// instance answers empty linkage (SRD-082 FR-7).
func TestHandleLinkageBeforeAdopt(t *testing.T) {
	h := &InstanceHandle{}
	require.Empty(t, h.ParentID())
	require.Empty(t, h.CallNodeID())
}

// TestReattachChildShapes drives the re-attach seam's three shapes
// directly (SRD-082 FR-7): no repository, an in-flight record (the
// lazy handle and its not-yet-tracked guards), and the terminal
// record's decode/outcome paths.
func TestReattachChildShapes(t *testing.T) {
	ctx := context.Background()

	t.Run("no repository is loud", func(t *testing.T) {
		th, err := New("ra-norepo",
			WithoutBanner(), WithoutStartupConfig())
		require.NoError(t, err)

		_, err = th.reattachChild("c-1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "no repository")
	})

	t.Run("an in-flight record yields the lazy handle", func(t *testing.T) {
		repo := memrepo.New()
		require.NoError(t, repo.RegisterGroup(ctx, "ra-lazy"))

		doc := &checkpoint.Document{
			InstanceID: "c-2", ProcessID: "p", Status: "Active",
		}
		payload, err := doc.Marshal()
		require.NoError(t, err)

		require.NoError(t, repo.Save(ctx, repository.InstanceRecord{
			ID: "c-2", Group: "ra-lazy",
			Status: repository.StatusActive, Payload: payload,
		}))

		th, err := New("ra-lazy",
			WithoutBanner(), WithoutStartupConfig(),
			WithRepository(repo), WithEngineGroup("ra-lazy"))
		require.NoError(t, err)

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		require.NoError(t, th.Run(runCtx))

		child, err := th.reattachChild("c-2")
		require.NoError(t, err)

		lc, ok := child.(*lazyChild)
		require.True(t, ok, "an unclaimed in-flight child re-attaches lazily")

		// the not-yet-tracked guards answer loud/zero, never wrong.
		require.Zero(t, lc.Version())
		require.Error(t, lc.Failed())

		_, err = lc.Outputs([]string{"x"})
		require.Error(t, err)

		lc.Terminate() // a no-op without a tracked instance

		select {
		case <-lc.Done():
			t.Fatal("an unclaimed child must not read settled")
		default:
		}
	})

	t.Run("a terminal record's outcome and outputs", func(t *testing.T) {
		require.NoError(t, data.CreateDefaultStates())

		// garbage payload refuses.
		_, err := newSettledChild("c-3", repository.InstanceRecord{
			ID: "c-3", Status: repository.StatusCompleted,
			Payload: []byte("not a checkpoint"),
		})
		require.Error(t, err)

		// a Terminated record maps to a call failure.
		doc := &checkpoint.Document{
			InstanceID: "c-4", ProcessID: "p", Status: "Terminated",
		}
		payload, err := doc.Marshal()
		require.NoError(t, err)

		terminated, err := newSettledChild("c-4", repository.InstanceRecord{
			ID: "c-4", Status: repository.StatusTerminated,
			Payload: payload,
		})
		require.NoError(t, err)
		require.Error(t, terminated.Failed())

		select {
		case <-terminated.Done():
		default:
			t.Fatal("a terminal handle is settled from birth")
		}

		// a Completed record serves its root-scope outputs.
		raw, err := checkpoint.EncodeData(ctx, "/p", []data.Data{
			data.MustParameter("result",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(42)),
					data.ReadyDataState)),
		})
		require.NoError(t, err)

		done := &checkpoint.Document{
			InstanceID: "c-5", ProcessID: "p", Status: "Completed",
			Scopes: []checkpoint.ScopeRecord{{Path: "/p", Data: raw}},
		}
		payload, err = done.Marshal()
		require.NoError(t, err)

		completed, err := newSettledChild("c-5", repository.InstanceRecord{
			ID: "c-5", Status: repository.StatusCompleted,
			Payload: payload,
		})
		require.NoError(t, err)
		require.NoError(t, completed.Failed())

		outs, err := completed.Outputs([]string{"result"})
		require.NoError(t, err)
		require.Len(t, outs, 1)

		_, err = completed.Outputs([]string{"absent"})
		require.Error(t, err)
	})
}
