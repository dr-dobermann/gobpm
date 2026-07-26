package instance

import (
	"github.com/dr-dobermann/gobpm/internal/scope"
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

// dataMovementKind selects the fact kind by the movement's target: a
// per-instance Data Object (SRD-063) or the engine-global Data Store (SRD-068).
var dataMovementKind = map[bool]observability.Kind{
	false: observability.KindDataObject,
	true:  observability.KindDataStore,
}

// dataMovementPhase selects the fact phase by direction: a read (data → Node) or
// a write (Node → data).
var dataMovementPhase = map[bool]observability.Phase{
	false: observability.PhaseRead,
	true:  observability.PhaseWritten,
}

// reportDataMovements publishes one fact per Data Object / Data Store read or
// write the node's data phases recorded on its frame (SRD-063 / SRD-068). These
// movements bypass the frame commit-diff (a Data Object write is an in-place
// update; a Data Store access never touches scope), so this is their only
// observability. KindDataObject is observer-only (the kindNoEcho flood guard,
// like KindDataChange); KindDataStore also echoes to the operator log.
func (t *track) reportDataMovements(node flow.Node, f *scope.Frame) {
	for _, m := range f.DataMovements() {
		details := map[string]string{observability.AttrDataName: m.Name}
		if m.EngineStore {
			details[observability.AttrDataStore] = m.StoreRef
		}

		t.instance.report(observability.Fact{
			Kind:     dataMovementKind[m.EngineStore],
			Phase:    dataMovementPhase[m.Write],
			NodeID:   node.ID(),
			NodeName: node.Name(),
			Details:  details,
		})
	}
}
