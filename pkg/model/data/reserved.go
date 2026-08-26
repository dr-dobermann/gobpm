package data

import (
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// The data names the ENGINE owns. A model may not declare any of them,
// because the engine publishes a value under each and a model's declaration
// of the same name would shadow it — silently, and with the wrong answer
// surfacing somewhere far from the declaration that caused it.
//
// Five are BPMN's own iteration attributes (Tables 10.27 and 10.30), spelled
// the standard's way. Four are the engine's, spelled the engine's way,
// because the standard has no word for them.
const (
	// LoopCounterName is BPMN's per-instance iteration ordinal (Table 10.27).
	LoopCounterName = "loopCounter"

	// NumberOfInstancesName and its three siblings are Table 10.30's
	// outer-instance counts.
	NumberOfInstancesName           = "numberOfInstances"
	NumberOfActiveInstancesName     = "numberOfActiveInstances"
	NumberOfCompletedInstancesName  = "numberOfCompletedInstances"
	NumberOfTerminatedInstancesName = "numberOfTerminatedInstances"

	// IterationNumberName, IterationIDName and IterationModeName are the
	// engine's per-execution names, published beside loopCounter.
	IterationNumberName = "ITERATION_NUMBER"
	IterationIDName     = "ITERATION_ID"
	IterationModeName   = "ITERATION_MODE"

	// IterationsName is the engine's durable per-activity register. It is
	// served from the reserved RUNTIME source rather than the data plane, so
	// a model could not reach it in any case — it is listed here so the whole
	// engine-owned vocabulary is refused in one place, and because a model
	// declaring it is confused about where the value lives either way.
	IterationsName = "ITERATIONS"
)

// reservedNames is the engine-owned set, as data. Lookup is a scan of nine
// entries at construction time, which is cheaper than the map it would
// otherwise be and keeps the list readable as a list.
var reservedNames = []string{
	LoopCounterName,
	NumberOfInstancesName,
	NumberOfActiveInstancesName,
	NumberOfCompletedInstancesName,
	NumberOfTerminatedInstancesName,
	IterationNumberName,
	IterationIDName,
	IterationModeName,
	IterationsName,
}

// ReservedNames returns the data names a model may not declare.
func ReservedNames() []string {
	return slices.Clone(reservedNames)
}

// CheckReservedName reports a classified error when name is one the engine
// publishes (see reservedNames). It guards the constructors that create a
// TOP-LEVEL data name — a Property, a Parameter, a DataObject, a
// DataStoreReference — because those are the names an expression resolves and
// a frame commits under, and so the ones that can shadow an engine value.
//
// It deliberately does NOT guard a structural field name (`order.loopCounter`):
// a field is reached through its owning value's name and can shadow nothing,
// so refusing it would reject a legitimate model for no gain.
//
// The check is unconditional rather than scoped to models that iterate: the
// names are engine-owned whether or not this particular process has a loop,
// and a refusal at the line that wrote the name is the whole point (ADR-025
// §2.9.2). A model wanting a datum of its own with such a meaning names it
// something else.
func CheckReservedName(name, errorClass string) error {
	trimmed := strings.TrimSpace(name)

	if !slices.Contains(reservedNames, trimmed) {
		return nil
	}

	return errs.New(
		errs.M("data name %q is published by the engine and cannot be "+
			"declared by a model", trimmed),
		errs.C(errorClass, errs.InvalidParameter))
}
