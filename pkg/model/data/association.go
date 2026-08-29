// Package data provides implementation of BPMN data elements including
// item definitions, data associations, properties, and formal expressions.
package data

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ============================================================================
//                          Association
// ============================================================================

// Data Associations are used to move data between Data Objects, Properties, and
// inputs and outputs of Activities, Processes, and GlobalTasks. Tokens do not
// flow along a Data Association, and as a result they have no direct effect on
// the flow of the Process. The purpose of retrieving data from Data Objects or
// Process Data Inputs is to fill the Activities inputs and later push the
// output values from the execution of the Activity back into Data Objects or
// Process Data Outputs.
//
// The core concepts of a DataAssociation are that they have sources, a target,
// and an optional transformation.
// When a data association is “executed,” data is copied to the target. What is
// copied depends if there is a transformation defined or not.
// If there is no transformation defined or referenced, then only one source
// MUST be defined, and the contents of this source will be copied into the
// target.
//
// If there is a transformation defined or referenced, then this transformation
// Expression will be evaluated and the result of the evaluation is copied into
// the target. There can be zero (0) to many sources defined in this case, but
// there is no requirement that these sources are used inside the Expression.
// In any case, sources are used to define if the data association can be
// “executed,” if any of the sources is in the state of “unavailable,” then the
// data association cannot be executed, and the Activity or Event where the data
// association is defined MUST wait until this condition is met.
// Data Associations are always contained within another element that defines
// when these data associations are going to be executed. Activities define two
// sets of data associations, while Events define only one.
// For Events, there is only one set, but they are used differently for catch or
// throw Events. For a catch Event, data associations are used to push data from
// the Message received into Data Objects and properties. For a throw Event,
// data associations are used to fill the Message that is being thrown.
// As DataAssociations are used in different stages of the Process and Activity
// lifecycle, the possible sources and targets vary according to that stage.
// This defines the scope of possible elements that can be referenced as
// source and target.
// For example: when an Activity starts executing, the scope of valid
// targets include the Activity data inputs, while at the end of the Activity
// execution, the scope of valid sources include Activity data outputs.

// Association represents a BPMN data association for moving data between elements.
type Association struct {
	transformation FormalExpression
	sources        map[string]*ItemAwareElement
	target         *ItemAwareElement
	// assignments are the from→to mappings of the association's second
	// expression shape (ADR-011 §2.4); empty unless the association
	// declares them, and never populated together with transformation.
	assignments  []*Assignment
	dataStoreRef string
	foundation.BaseElement
}

// Transformation returns the association's transformation expression, or nil
// when it carries none. Its result REPLACES the target's value (§10.4.2
// rule 1).
func (a *Association) Transformation() FormalExpression {
	return a.transformation
}

// Assignments returns the association's from→to mappings, in declaration
// order — a copy, so a caller cannot reshape the association. Empty unless
// the association declares them.
func (a *Association) Assignments() []*Assignment {
	if len(a.assignments) == 0 {
		return nil
	}

	return append(make([]*Assignment, 0, len(a.assignments)), a.assignments...)
}

// DataStoreRef returns the engine Data Store id when the Association is backed
// by a DataStoreReference (SRD-068 FR-4) — the task reroute resolves that store
// from the engine registry and reads/writes it by the association's item name.
// Empty for a scope-backed (DataObject / activity-I/O) association, which routes
// through per-instance scope.
func (a *Association) DataStoreRef() string {
	return a.dataStoreRef
}

// TargetName returns the name of the association's target item-aware element —
// for a DataObject output association (Node → DataObject) this is the
// DataObject's scope name, by which the runtime resolves the per-instance
// DataObject (SRD-063 FR-5).
func (a *Association) TargetName() string {
	return a.target.Name()
}

// SourceNames returns the names of the association's source item-aware elements
// — for a DataObject input association (DataObject → Node) the single source is
// the DataObject, so its scope name is returned (SRD-063 FR-5).
func (a *Association) SourceNames() []string {
	names := make([]string, 0, len(a.sources))
	for _, s := range a.sources {
		names = append(names, s.Name())
	}

	return names
}

// NewAssociation creates a new data Association with the specified target.
func NewAssociation(
	target *ItemAwareElement,
	opts ...options.Option,
) (*Association, error) {
	if target == nil {
		return nil, errs.New(
			errs.M("no target"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	aCfg := asscConfig{
		trg: target,
	}

	ee := []error{}

	if err := aCfg.trg.UpdateState(UnavailableDataState); err != nil {
		ee = append(ee, err)
	}

	for _, o := range opts {
		switch opt := o.(type) {
		case asscOption:
			if err := opt(&aCfg); err != nil {
				ee = append(ee, err)
			}

		case foundation.BaseOption:
			aCfg.baseOptions = append(aCfg.baseOptions, o)

		default:
			ee = append(ee,
				fmt.Errorf("invalid option type: %s", reflect.TypeOf(o).String()))
		}
	}

	if len(ee) != 0 {
		return nil,
			errs.New(
				errs.M("association building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(errors.Join(ee...)))
	}

	return aCfg.newAssociation()
}

// IsReady reports whether the association's target holds a Ready value. It
// is the only read of the target's state on this type, and the association
// keeps the target private.
func (a *Association) IsReady() bool {
	if a.target == nil {
		return false
	}

	return a.target.State().Name() == ReadyDataState.Name()
}

// TargetItemDefID returns id of the Association's target ItemDefiniiton.
func (a *Association) TargetItemDefID() string {
	if a.target == nil {
		return ""
	}

	return a.target.ItemDefinition().ID()
}

// SourcesIDs returns list of the Association's sources ItemDefinitions Ids.
func (a *Association) SourcesIDs() []string {
	srcIDs := make([]string, 0, len(a.sources))
	for k := range a.sources {
		srcIDs = append(srcIDs, k)
	}

	return srcIDs
}

// HasSourceID checks if the Association has source with Id id.
func (a *Association) HasSourceID(id string) bool {
	_, ok := a.sources[id]
	return ok
}

// --------------------------- Source interface -------------------------------

// Find looks for the source whose ItemDefinition Id equals the name's head and
// returns it (only when Ready), navigating any structural steps into it
// (ADR-011 v.6 §2.9.2). A plain name returns the source unchanged.
func (a *Association) Find(ctx context.Context, name string) (Data, error) {
	return ResolvePath(ctx, name, func(head string) (Data, error) {
		src, ok := a.sources[head]
		if !ok {
			return nil, fmt.Errorf("no source #%s", head)
		}

		if src.dataState.name != ReadyDataState.name {
			return nil,
				fmt.Errorf(
					"source #%s isn't in Ready state (actual state is %s)",
					src.subject.ID(), src.dataState.name)
		}

		return src, nil
	})
}

// -----------------------------------------------------------------------------
