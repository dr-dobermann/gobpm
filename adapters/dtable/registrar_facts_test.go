package dtable_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// reporterSink collects facts reported by the engine's registrar surfaces.
type reporterSink struct {
	facts []observability.Fact
}

func (rs *reporterSink) Report(f observability.Fact) {
	rs.facts = append(rs.facts, f)
}

// TestRegistrarFacts covers SRD-069 T-4's unit half: Register and Deploy
// emit the audit facts once bound — names and counts only — and stay
// silent unbound.
func TestRegistrarFacts(t *testing.T) {
	t.Run("unbound register and deploy are silent",
		func(t *testing.T) {
			dec := &switchDecoder{tbl: oneRowTable(t, "quiet", 1)}

			e, err := dtable.New(dtable.WithDecoder(dec))
			require.NoError(t, err)

			e.BindReporter(nil) // ignored

			require.NoError(t, e.Register(oneRowTable(t, "r", 1)))
			require.NoError(t, e.Deploy(ctx, []byte(`v1`)))
		})

	t.Run("bound register emits Registered with the rule count",
		func(t *testing.T) {
			sink := &reporterSink{}

			e, err := dtable.New()
			require.NoError(t, err)

			e.BindReporter(sink)
			require.NoError(t, e.Register(oneRowTable(t, "discount", 1)))

			require.Len(t, sink.facts, 1)
			f := sink.facts[0]
			require.Equal(t, observability.KindRules, f.Kind)
			require.Equal(t, observability.PhaseRegistered, f.Phase)
			require.Equal(t, "discount",
				f.Details[observability.AttrDecisionRef])
			require.Equal(t, dtable.DTableType,
				f.Details[observability.AttrImplementation])
			require.Equal(t, "1",
				f.Details[observability.AttrRuleCount])
		})

	t.Run("a rejected duplicate emits nothing",
		func(t *testing.T) {
			sink := &reporterSink{}

			e, err := dtable.New()
			require.NoError(t, err)

			e.BindReporter(sink)
			require.NoError(t, e.Register(oneRowTable(t, "once", 1)))
			require.Error(t, e.Register(oneRowTable(t, "once", 1)))

			require.Len(t, sink.facts, 1, "only the success is audited")
		})

	t.Run("deploy emits Deployed and flags the replacement",
		func(t *testing.T) {
			dec := &switchDecoder{tbl: oneRowTable(t, "d", 1)}

			e, err := dtable.New(dtable.WithDecoder(dec))
			require.NoError(t, err)

			sink := &reporterSink{}
			e.BindReporter(sink)

			require.NoError(t, e.Deploy(ctx, []byte(`v1`)))

			dec.tbl = oneRowTable(t, "d", 2)
			require.NoError(t, e.Deploy(ctx, []byte(`v2`)))

			require.Len(t, sink.facts, 2)

			fresh := sink.facts[0]
			require.Equal(t, observability.PhaseDeployed, fresh.Phase)
			require.Equal(t, "false", fresh.Details["replaced"])

			redeploy := sink.facts[1]
			require.Equal(t, observability.PhaseDeployed, redeploy.Phase)
			require.Equal(t, "true", redeploy.Details["replaced"])
			require.Equal(t, "d",
				redeploy.Details[observability.AttrDecisionRef])
		})
}

// TestDeployAuditE2E covers SRD-069 T-4's e2e half: thresher.New binds its
// producer into the dtable engine, and a runtime Deploy lands on the
// engine-wide observer stream as the Deployed governance fact.
func TestDeployAuditE2E(t *testing.T) {
	dec := &switchDecoder{tbl: oneRowTable(t, "discount", 25)}

	e, err := dtable.New(dtable.WithDecoder(dec))
	require.NoError(t, err)

	th, err := thresher.New("test-deploy-audit",
		thresher.WithoutBanner(), thresher.WithRuleEngine(e))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	c := &collector{}
	sub := th.Observe(c)
	defer sub.Cancel()

	require.NoError(t, e.Deploy(ctx, []byte(`v1`)))

	require.Eventually(t, func() bool {
		f, ok := c.rulesFact(observability.PhaseDeployed)

		return ok &&
			f.Details[observability.AttrDecisionRef] == "discount" &&
			f.Details["replaced"] == "false"
	}, 2*time.Second, 10*time.Millisecond,
		"the runtime deploy must reach the engine-wide observer")
}
