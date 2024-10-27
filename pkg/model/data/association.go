package data

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

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

// ============================================================================
//                          Association
// ============================================================================

type Association struct {
	foundation.BaseElement

	// Specifies an optional transformation Expression. The actual scope of
	// accessible data for that Expression is defined by the source and target
	// of the specific Data Association types.
	Transformation FormalExpression

	// Specifies one or more data elements Assignments. By using an Assignment,
	// single data structure elements can be assigned from the source structure
	// to the target structure.
	Assignments []*Assignment

	// Identifies the source of the Data Association. The source MUST be an
	// ItemAwareElement.
	sources map[string]*ItemAwareElement

	// Identifies the target of the Data Association. The target MUST be an
	// ItemAwareElement.
	target *ItemAwareElement
}

// UpdateSource updates source with a new value and recalculate value of
// the association target if it's possible.
func (a *Association) UpdateSource(iDef *ItemDefinition) error {
	if iDef == nil {
		return errs.New(
			errs.M("empty itemDefinition"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	iae, ok := a.sources[iDef.Id()]
	if !ok {
		return errs.New(
			errs.M("invalid source"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("association_id", a.Id()),
			errs.D("source_id", iDef.Id()))
	}

	if err := iae.Value().Update(iDef.structure.Get()); err != nil {
		return errs.New(
			errs.M("source updating failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err),
			errs.D("association_id", a.Id()))
	}

	return a.calculate()
}

// IsReady checks if the Association's target is ready.
func (a *Association) IsReady() bool {
	if a.target == nil {
		return false
	}

	return a.target.State().Name() == ReadyDataState.Name()
}

// Value returns IDef's value of the association's target if
// it's in Ready state.
func (a *Association) Value() (*ItemDefinition, error) {
	if a.target == nil {
		return nil,
			errs.New(
				errs.M("association #%s target isn't defined", a.Id()))
	}

	if a.target.dataState.Name() != ReadyDataState.Name() {
		return nil,
			errs.New(
				errs.M("target isn't in Ready state"),
				errs.C(errorClass, errs.InvalidState))
	}

	return a.target.Subject(), nil
}

// TargetItemDefId returns id of the Association's target ItemDefiniiton.
func (a *Association) TargetItemDefId() string {
	if a.target == nil {
		return ""
	}

	return a.target.ItemDefinition().Id()
}

// SourcesIds returns list of the Association's sources ItemDefinitions Ids.
func (a *Association) SourcesIds() []string {
	src := []string{}
	for _, s := range a.sources {
		src = append(src, s.Subject().Id())
	}

	return src
}

// HasSourceId checks if the Association has source with Id id.
func (a *Association) HasSourceId(id string) bool {
	for _, s := range a.sources {
		if s.Subject().Id() == id {
			return true
		}
	}

	return false
}

// calculate actualizes target based on current source value.
// if there is no readness of source error isn't occured and
// associateion target state becomes Unavailable.
// calculate returns error only if transformation or assignment are
// failed.
func (a *Association) calculate() error {
	return fmt.Errorf("not implemented yet")
}

// ============================================================================
//                          Assignment
// ============================================================================

// The Assignment class is used to specify a simple mapping of data elements
// using a specified Expression language.
// The default Expression language for all Expressions is specified in the
// Definitions element, using the expressionLanguage attribute. It can also be
// overridden on each individual Assignment using the same attribute.
type Assignment struct {
	foundation.BaseElement

	// The Expression that evaluates the source of the Assignment.
	From FormalExpression

	// The Expression that defines the actual Assignment operation and the
	// target data element.
	To FormalExpression
}
