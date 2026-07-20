package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// linkThrow builds a Link intermediate throw source named id carrying link name.
func linkThrow(t *testing.T, id, name string) flow.Node {
	t.Helper()

	ite, err := events.NewIntermediateThrowEvent(
		id, events.MustLinkEventDefinition(name))
	require.NoError(t, err)

	return ite
}

// linkCatch builds a Link intermediate catch target named id carrying link name.
func linkCatch(t *testing.T, id, name string) flow.Node {
	t.Helper()

	ice, err := events.NewIntermediateCatchEvent(
		id, events.MustLinkEventDefinition(name))
	require.NoError(t, err)

	return ice
}

// TestLinkEventDefinition covers the LinkEventDefinition model (SRD-057 T-1,
// FR-1).
func TestLinkEventDefinition(t *testing.T) {
	t.Run("a name is required", func(t *testing.T) {
		_, err := events.NewLinkEventDefinition("")
		require.Error(t, err)

		_, err = events.NewLinkEventDefinition("   ") // blank trims to empty
		require.Error(t, err)
	})

	t.Run("a bad base option is rejected", func(t *testing.T) {
		// WithName is not a valid base option for an event definition — the
		// embedded base-element build fails and propagates out.
		_, err := events.NewLinkEventDefinition("ok", options.WithName("bad"))
		require.Error(t, err)
	})

	t.Run("valid name — Type and Name", func(t *testing.T) {
		led, err := events.NewLinkEventDefinition("  retry  ")
		require.NoError(t, err)
		require.Equal(t, flow.TriggerLink, led.Type())
		require.Equal(t, "retry", led.Name()) // trimmed
	})

	t.Run("Must panics on empty, returns on valid", func(t *testing.T) {
		require.Panics(t, func() { events.MustLinkEventDefinition("") })
		require.NotNil(t, events.MustLinkEventDefinition("ok"))
	})

	t.Run("satisfies flow.EventDefinition", func(t *testing.T) {
		var _ flow.EventDefinition = events.MustLinkEventDefinition("x")
	})
}

// TestLinkEventPositions covers the accepted/rejected positions (SRD-057 T-2,
// FR-2).
func TestLinkEventPositions(t *testing.T) {
	t.Run("a Link intermediate throw constructs", func(t *testing.T) {
		ite, err := events.NewIntermediateThrowEvent(
			"go", events.MustLinkEventDefinition("L"))
		require.NoError(t, err)
		require.Equal(t, flow.IntermediateEventClass, ite.EventClass())
	})

	t.Run("a Link intermediate catch constructs", func(t *testing.T) {
		ice, err := events.NewIntermediateCatchEvent(
			"target", events.MustLinkEventDefinition("L"))
		require.NoError(t, err)
		require.Equal(t, flow.IntermediateEventClass, ice.EventClass())
	})

	t.Run("a Link boundary event is rejected", func(t *testing.T) {
		host := boundaryHostTask(t)

		_, err := events.NewBoundaryEvent(
			"b", host, events.MustLinkEventDefinition("L"), true)
		require.Error(t, err)
	})
}

// TestValidateLinkPairing covers the per-container pairing check directly
// (SRD-057 T-3, FR-3).
func TestValidateLinkPairing(t *testing.T) {
	t.Run("one source, one target — ok", func(t *testing.T) {
		require.NoError(t, events.ValidateLinkPairing([]flow.Node{
			linkThrow(t, "s", "L"), linkCatch(t, "t", "L"),
		}))
	})

	t.Run("many sources, one target — ok", func(t *testing.T) {
		require.NoError(t, events.ValidateLinkPairing([]flow.Node{
			linkThrow(t, "s1", "L"), linkThrow(t, "s2", "L"),
			linkCatch(t, "t", "L"),
		}))
	})

	t.Run("no nodes / no link nodes — ok", func(t *testing.T) {
		require.NoError(t, events.ValidateLinkPairing(nil))
	})

	t.Run("a source with no target errs", func(t *testing.T) {
		err := events.ValidateLinkPairing([]flow.Node{linkThrow(t, "s", "L")})
		require.ErrorContains(t, err, "L")
		require.ErrorContains(t, err, "no target catch")
	})

	t.Run("two targets errs (ambiguous)", func(t *testing.T) {
		err := events.ValidateLinkPairing([]flow.Node{
			linkThrow(t, "s", "L"),
			linkCatch(t, "t1", "L"), linkCatch(t, "t2", "L"),
		})
		require.ErrorContains(t, err, "expected exactly one")
	})

	t.Run("a lone target with no source errs", func(t *testing.T) {
		err := events.ValidateLinkPairing([]flow.Node{linkCatch(t, "t", "L")})
		require.ErrorContains(t, err, "no source throw")
	})

	t.Run("distinct names are independent; one bad name reported", func(t *testing.T) {
		err := events.ValidateLinkPairing([]flow.Node{
			linkThrow(t, "s1", "ok"), linkCatch(t, "t1", "ok"), // paired
			linkThrow(t, "s2", "bad"), // no target
		})
		require.ErrorContains(t, err, "bad")
		require.NotContains(t, err.Error(), `"ok"`)
	})

	t.Run("non-Link nodes are ignored", func(t *testing.T) {
		// a non-event node (start), and an intermediate throw carrying a
		// NON-Link (signal) definition, mixed with a valid Link pair.
		start, err := events.NewStartEvent("start")
		require.NoError(t, err)

		sigThrow, err := events.NewIntermediateThrowEvent(
			"sig", signalDef(t, "s"))
		require.NoError(t, err)

		require.NoError(t, events.ValidateLinkPairing([]flow.Node{
			start, sigThrow,
			linkThrow(t, "s1", "L"), linkCatch(t, "t1", "L"),
		}))
	})
}

// TestLinkThrowRedirect covers the Link throw's Exec redirect and the
// flow.LinkEventNode/LinkSource surface (SRD-057 M2, FR-5).
func TestLinkThrowRedirect(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("Exec returns the resolved target catch's outgoing flows",
		func(t *testing.T) {
			cat, err := events.NewIntermediateCatchEvent(
				"cat", events.MustLinkEventDefinition("go"))
			require.NoError(t, err)

			end, err := events.NewEndEvent("end")
			require.NoError(t, err)

			f, err := flow.Link(cat, end) // the catch's downstream
			require.NoError(t, err)

			thr, err := events.NewIntermediateThrowEvent(
				"thr", events.MustLinkEventDefinition("go"))
			require.NoError(t, err)

			thr.SetLinkTarget(cat) // what the graph wiring does

			got, err := thr.Exec(ctx, nil) // the Link path never touches renv
			require.NoError(t, err)
			require.Equal(t, []*flow.SequenceFlow{f}, got)
		})

	t.Run("an unresolved Link throw Exec errs", func(t *testing.T) {
		thr, err := events.NewIntermediateThrowEvent(
			"thr", events.MustLinkEventDefinition("x"))
		require.NoError(t, err)

		_, err = thr.Exec(ctx, nil)
		require.ErrorContains(t, err, "no resolved target")
	})

	t.Run("Link node predicates on throw and catch", func(t *testing.T) {
		thr := linkThrow(t, "t", "n")
		cat := linkCatch(t, "c", "n")

		require.Equal(t, "n", thr.(flow.LinkEventNode).LinkName())
		require.True(t, thr.(flow.LinkEventNode).IsLinkSource())
		require.Equal(t, "n", cat.(flow.LinkEventNode).LinkName())
		require.False(t, cat.(flow.LinkEventNode).IsLinkSource())

		// a non-Link throw has no link name
		sig, err := events.NewIntermediateThrowEvent("sig", signalDef(t, "s"))
		require.NoError(t, err)
		require.Equal(t, "", sig.LinkName())
	})
}
