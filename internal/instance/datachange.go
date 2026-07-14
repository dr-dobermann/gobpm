package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// dataChangePhase maps the commit-diff's change-kind vocabulary onto the
// observability DataChange phases — the two sets carry the same wire names by
// design (ADR-013 v.2 mirrors data.ChangeType).
var dataChangePhase = map[data.ChangeType]observability.Phase{
	data.ValueAdded:   observability.PhaseValueAdded,
	data.ValueUpdated: observability.PhaseValueUpdated,
	data.ValueDeleted: observability.PhaseValueDeleted,
}

// reportDataChanges publishes one DataChange fact per committed changed path
// (SRD-044 FR-4, ADR-011 v.6 §2.9.4): the activity-boundary change signal a
// node's frame commit produced, attributed to the committing node. DataChange
// is observer-only (no operator-log echo — the kindNoEcho flood guard);
// Instance.report's no-listener guard keeps the no-observer path cheap.
func (t *track) reportDataChanges(node flow.Node, changes []data.Change) {
	for _, c := range changes {
		t.instance.report(observability.Fact{
			Kind:     observability.KindDataChange,
			Phase:    dataChangePhase[c.Type],
			NodeID:   node.ID(),
			NodeName: node.Name(),
			Details: map[string]string{
				observability.AttrDataPath: c.Path,
			},
		})
	}
}
