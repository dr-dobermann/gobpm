package thresher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

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
