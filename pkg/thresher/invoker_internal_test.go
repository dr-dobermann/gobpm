package thresher

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// residentInstance mints a real instance and tracks it on the thresher,
// the way every launch path does — the re-attach seam's resident branch
// needs an entry in t.instances, not a running process.
func residentInstance(t *testing.T, th *Thresher) *instance.Instance {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	s, err := snapshot.New(noneStartProcess(t, "p-resident"))
	require.NoError(t, err)

	inst, err := instance.New(s, scope.EmptyDataPath, &th.cfg, th, th.taskDist)
	require.NoError(t, err)

	th.trackInstanceLocked(inst, func() {}, th.settledFor(inst.ID()))

	return inst
}

// TestReattachChildResident covers the seam's first shape (SRD-082
// FR-7): the child never left (or was already recovered), so re-attach
// returns a live handle without touching the repository.
func TestReattachChildResident(t *testing.T) {
	th, err := New("reattach-resident")
	require.NoError(t, err)

	inst := residentInstance(t, th)

	cp, err := th.reattachChild(inst.ID())
	require.NoError(t, err)
	require.Equal(t, inst.ID(), cp.ID())
}

// TestReattachChildEngineNotRunning: a repository is configured but the
// engine holds no context — re-attach refuses instead of loading with a
// nil context.
func TestReattachChildEngineNotRunning(t *testing.T) {
	th, err := New("reattach-stopped", WithRepository(memrepo.New()))
	require.NoError(t, err)

	_, err = th.reattachChild("ghost")
	require.ErrorContains(t, err, "isn't running")
}

// loadBoomRepo fails every Load; the rest is a real memrepo, so engine
// startup (group registration, the recovery listing) works.
type loadBoomRepo struct{ repository.Repository }

func (loadBoomRepo) Load(
	_ context.Context, _ string,
) (repository.InstanceRecord, bool, error) {
	return repository.InstanceRecord{}, false, fmt.Errorf("load boom")
}

// TestReattachChildLoadError: a repository failure during re-attach
// surfaces to the caller unchanged.
func TestReattachChildLoadError(t *testing.T) {
	th, err := New("reattach-load-err",
		WithRepository(loadBoomRepo{memrepo.New()}))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.reattachChild("ghost")
	require.ErrorContains(t, err, "load boom")
}

// TestLazyChildResidentPaths covers the lazy handle's delegation once
// the child became resident: Outputs routes through a live handle, and
// Terminate cancels the instance (a no-op for an untracked id).
func TestLazyChildResidentPaths(t *testing.T) {
	th, err := New("lazy-resident")
	require.NoError(t, err)

	inst := residentInstance(t, th)

	lc := &lazyChild{thr: th, id: inst.ID(),
		settled: th.settledFor(inst.ID())}

	out, err := lc.Outputs(nil)
	require.NoError(t, err)
	require.Empty(t, out)

	lc.Terminate()

	(&lazyChild{thr: th, id: "ghost"}).Terminate()
}

// terminalRecord wraps a checkpoint document into the repository record
// newSettledChild consumes.
func terminalRecord(
	t *testing.T, doc *checkpoint.Document,
) repository.InstanceRecord {
	t.Helper()

	raw, err := doc.Marshal()
	require.NoError(t, err)

	return repository.InstanceRecord{
		ID: doc.InstanceID, Payload: raw,
		Status: repository.StatusCompleted,
	}
}

// TestSettledChildSkipsNonRootScopes: only the root scope's data feeds
// the outputs — nested scopes and data-less scopes are skipped, and a
// missing declared output then refuses by name. Terminate on a settled
// child is a no-op.
func TestSettledChildSkipsNonRootScopes(t *testing.T) {
	rec := terminalRecord(t, &checkpoint.Document{
		InstanceID: "kid", ProcessID: "p", Status: "Completed",
		Scopes: []checkpoint.ScopeRecord{
			{Path: "/p/sub", Data: json.RawMessage(`[]`)},
			{Path: "/p"},
		},
	})

	cp, err := newSettledChild("kid", rec)
	require.NoError(t, err)
	require.NoError(t, cp.Failed())

	_, err = cp.Outputs([]string{"x"})
	require.ErrorContains(t, err, "no declared output")

	cp.Terminate()
}

// TestSettledChildRootDataUndecodable: a terminal record whose root
// scope data does not decode is a loud build failure, not an empty
// output set.
func TestSettledChildRootDataUndecodable(t *testing.T) {
	rec := terminalRecord(t, &checkpoint.Document{
		InstanceID: "kid", ProcessID: "p", Status: "Completed",
		Scopes: []checkpoint.ScopeRecord{
			{Path: "/p", Data: json.RawMessage(`42`)},
		},
	})

	_, err := newSettledChild("kid", rec)
	require.ErrorContains(t, err, "root data doesn't decode")
}
