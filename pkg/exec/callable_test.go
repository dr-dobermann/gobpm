package exec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/stretchr/testify/require"
)

// TestDefaultCallableResolver is SRD-096 T-3: the resolver the engine uses
// when the host supplies none.
//
// Its whole job is to keep today's behaviour exact for the unqualified case
// and to refuse the qualified one BY NAME rather than guess. Both halves are
// asserted, because the interesting failure is not an error where one is
// expected — it is a plausible answer where there is no right one: taking the
// local part of a qualified reference would call whatever the host happened to
// register under a coinciding name, silently.
func TestDefaultCallableResolver(t *testing.T) {
	var r exec.DefaultCallableResolver

	t.Run("an unqualified reference is its own key", func(t *testing.T) {
		key, err := r.ResolveCallable(context.Background(),
			exec.CallableRef{Key: "charge"})
		require.NoError(t, err)
		require.Equal(t, "charge", key,
			"the reference IS the key when nothing qualifies it — a host "+
				"that never imports across documents configures nothing")
	})

	t.Run("a qualified reference is refused, naming both halves",
		func(t *testing.T) {
			key, err := r.ResolveCallable(context.Background(),
				exec.CallableRef{
					Namespace: "http://example.com/shared",
					Key:       "audit",
				})
			require.Error(t, err,
				"guessing a key for a namespace nobody mapped would call "+
					"whatever coincides with the local part")
			require.Empty(t, key)

			for _, want := range []string{
				"http://example.com/shared", "audit", "WithCallableResolver",
			} {
				require.Truef(t, strings.Contains(err.Error(), want),
					"error %q must name %q — the message is how a host "+
						"learns which namespace to teach the engine about",
					err, want)
			}
		})
}

// TestCallableResolverFunc covers the adapter a host with a one-line mapping
// uses instead of declaring a type.
func TestCallableResolverFunc(t *testing.T) {
	var seen exec.CallableRef

	f := exec.CallableResolverFunc(
		func(_ context.Context, ref exec.CallableRef) (string, error) {
			seen = ref

			return "shared." + ref.Key, nil
		})

	key, err := f.ResolveCallable(context.Background(), exec.CallableRef{
		Namespace: "http://example.com/shared",
		Key:       "audit",
	})
	require.NoError(t, err)
	require.Equal(t, "shared.audit", key)
	require.Equal(t, "http://example.com/shared", seen.Namespace,
		"the resolver sees the namespace — routing by it is the whole reason "+
			"the reference carries one")
}

// TestCallableResolverFuncRefusesNil pins the trap a typed nil sets: a
// CallableResolverFunc(nil) is a NON-nil CallableResolver, so it walks
// straight through thresher.WithCallableResolver's `r == nil` guard. Without
// the check inside ResolveCallable the mistake surfaces as a panic at the
// first call — inside the engine, on a goroutine, arbitrarily far from the
// WithCallableResolver line that caused it.
func TestCallableResolverFuncRefusesNil(t *testing.T) {
	var f exec.CallableResolverFunc // nil, and a valid CallableResolver

	// The plain comparison an option's guard makes — NOT testify's NotNil,
	// which reflects into the interface and reports the typed nil as nil.
	// What thresher.WithCallableResolver evaluates is this one, and this one
	// is false, which is exactly why the value gets through.
	if r := exec.CallableResolver(f); r == nil {
		t.Fatal("a typed nil compared equal to nil — then the option's own " +
			"guard would have caught it and this test is pinning nothing")
	}

	key, err := f.ResolveCallable(context.Background(),
		exec.CallableRef{Key: "audit"})

	require.Error(t, err, "a nil function must be refused, never invoked")
	require.Empty(t, key)
	require.Contains(t, err.Error(), "CallableResolverFunc",
		"the message must name the offending parameter, so a host reading it "+
			"knows which of its options was wrong")
	require.Contains(t, err.Error(), "audit",
		"and which callable went unresolved")
}

// TestDefaultCallableResolverRefusesEmptyKey covers the other half of the
// default resolver's contract. An unqualified reference IS the key it
// answers, so an empty one would answer an empty key: the registry lookup
// then fails naming nothing, and the caller cannot tell which end was wrong.
func TestDefaultCallableResolverRefusesEmptyKey(t *testing.T) {
	key, err := exec.DefaultCallableResolver{}.ResolveCallable(
		context.Background(), exec.CallableRef{})

	require.Error(t, err)
	require.Empty(t, key, "an empty key must never be answered as if resolved")
	require.Contains(t, err.Error(), "ref.Key",
		"the message names the parameter at fault")
}
