package thresher

import (
	"context"
	"errors"
	"testing"
	"time"

	gerrs "github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/stretchr/testify/require"
)

// claimStubRepo hands recoverOne a claimable record and fails the claim Save
// with a chosen error, so the two failures that used to be indistinguishable
// can be driven apart.
type claimStubRepo struct {
	repository.Repository

	rec     repository.InstanceRecord
	saveErr error
}

func (r *claimStubRepo) Load(
	_ context.Context, _ string,
) (repository.InstanceRecord, bool, error) {
	return r.rec, true, nil
}

func (r *claimStubRepo) Save(
	_ context.Context, _ repository.InstanceRecord,
) error {
	return r.saveErr
}

// TestLostClaimClassification pins the distinction FIX-038 §1.4 turns on: the
// Repository contract requires a compare-and-set mismatch to carry
// errs.ConcurrentUpdate, and nothing else may be read as one.
func TestLostClaimClassification(t *testing.T) {
	require.True(t, lostClaim(gerrs.New(
		gerrs.M("version mismatch"),
		gerrs.C(errorClass, gerrs.ConcurrentUpdate))),
		"a CAS conflict is a lost claim")

	require.False(t, lostClaim(errors.New("connection reset by peer")),
		"a transport error is NOT a lost claim")

	require.False(t, lostClaim(gerrs.New(
		gerrs.M("the store is unavailable"),
		gerrs.C(errorClass, gerrs.OperationFailed))),
		"another classified failure is NOT a lost claim")
}

// TestRecoveryReportsATransportError is FIX-038 T-4. recoverOne read EVERY Save
// failure as "someone else claimed it" and returned nil, so a connection reset
// at startup silently abandoned an in-flight instance — and because it reported
// success, recoverInstances logged nothing either. A lost claim must stay
// silent; anything else must be reported.
func TestRecoveryReportsATransportError(t *testing.T) {
	base := memrepo.New()

	// A record whose lease has lapsed: claimable, so recoverOne reaches Save.
	// The group defaults to the engine id when WithEngineGroup is not given
	// (thresher.go:241), so each sub-test names its record's group to match.
	newRec := func(group string) repository.InstanceRecord {
		return repository.InstanceRecord{
			ID:    "i-recover",
			Group: group,
			Lease: repository.Lease{
				Owner:  "gone",
				Expiry: time.Now().Add(-time.Hour),
			},
		}
	}

	t.Run("a lost CAS stays silent", func(t *testing.T) {
		th, err := New("rec-cas", WithoutBanner(), WithoutStartupConfig(),
			WithRepository(&claimStubRepo{
				Repository: base,
				rec:        newRec("rec-cas"),
				saveErr: gerrs.New(
					gerrs.M("stored version moved"),
					gerrs.C(errorClass, gerrs.ConcurrentUpdate)),
			}))
		require.NoError(t, err)

		claimed, rerr := th.recoverOne(context.Background(), "i-recover",
			map[string]struct{}{})
		require.NoError(t, rerr,
			"another engine recovered it — that is not this engine's failure")
		require.False(t, claimed,
			"and the record stays that engine's, not ours")
	})

	t.Run("a transport error is reported", func(t *testing.T) {
		th, err := New("rec-transport", WithoutBanner(), WithoutStartupConfig(),
			WithRepository(&claimStubRepo{
				Repository: base,
				rec:        newRec("rec-transport"),
				saveErr:    errors.New("connection reset by peer"),
			}))
		require.NoError(t, err)

		_, err = th.recoverOne(context.Background(), "i-recover",
			map[string]struct{}{})
		require.Error(t, err,
			"a store failure must not be mistaken for a lost claim")
		require.ErrorContains(t, err, "couldn't claim the record")
	})
}
