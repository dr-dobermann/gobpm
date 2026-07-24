package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// TestExecEnvReporterRoutesThroughInstance covers the execEnv Reporter
// override (SRD-060 FR-6 infrastructure): a node-side fact must go through
// the instance's single emission point — reaching the instance's local
// observers with the instance_id stamp — not the raw engine sink.
func TestExecEnvReporterRoutesThroughInstance(t *testing.T) {
	inst := &Instance{}

	var got []observability.Fact

	inst.AddObserver(func(ev observability.Fact) {
		got = append(got, ev)
	})

	e := newExecEnv(inst, nil, nil)

	e.Reporter().Report(observability.Fact{
		Kind:  observability.KindRules,
		Phase: observability.PhaseEvaluated,
	})

	require.Len(t, got, 1)
	require.Equal(t, observability.KindRules, got[0].Kind)
	require.Equal(t, observability.PhaseEvaluated, got[0].Phase)
	require.Equal(t, inst.ID(),
		got[0].Details[observability.AttrInstanceID],
		"the emission point must stamp the instance id")
}
