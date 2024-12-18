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

const (
	Recalculate   bool = true
	NoRecalculate      = false
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

type Association struct {
	foundation.BaseElement

	// Specifies an optional transformation Expression. The actual scope of
	// accessible data for that Expression is defined by the source and target
	// of the specific Data Association types.
	transformation FormalExpression

	// Specifies one or more data elements Assignments. By using an Assignment,
	// single data structure elements can be assigned from the source structure
	// to the target structure.
	//
	// DEV-NOTE: Standard doesn't tell if the assignment should be used in
	// conjunction with transformation or in case when no transformation is
	// defined.
	// In my opinion any assignment could be easily made in transformation.
	// That's why I delete assignment from the association.
	//
	// Assignments []*Assignment

	// Identifies the source of the Data Association. The source MUST be an
	// ItemAwareElement.
	//
	// Map is indexed by itemDefinition id of ItemAwareElement.
	sources map[string]*ItemAwareElement

	// Identifies the target of the Data Association. The target MUST be an
	// ItemAwareElement.
	target *ItemAwareElement
}

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
			if err := opt.Apply(&aCfg); err != nil {
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

// UpdateSource updates association source and target with a new value.
func (a *Association) UpdateSource(
	ctx context.Context,
	iDef *ItemDefinition,
	recalculate bool,
) error {
	if iDef == nil {
		return errs.New(
			errs.M("empty itemDefinition"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := a.updateSrc(ctx, iDef); err != nil {
		return errs.New(
			errs.M("source updating failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err),
			errs.D("source_id", iDef.Id()),
			errs.D("association_id", a.Id()))
	}

	if err := a.target.UpdateState(UnavailableDataState); err != nil {
		return errs.New(
			errs.M("association target state update failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("association_id", a.Id()),
			errs.E(err))
	}

	if recalculate {
		if err := a.calculate(ctx); err != nil {
			return errs.New(
				errs.M("association target value recalculation failed"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("association_id", a.Id()),
				errs.E(err))
		}
	}

	return nil
}

// updateSrc updates value and state of single source.
func (a *Association) updateSrc(
	ctx context.Context,
	iDef *ItemDefinition,
) error {
	// find correlated source ItemAwareElement
	iae, ok := a.sources[iDef.Id()]
	if !ok {
		return fmt.Errorf("source isn't found in association")
	}

	// update source and its status
	if err := iae.Value().Update(ctx, iDef.structure.Get(ctx)); err != nil {
		return fmt.Errorf("source updating failed: %w", err)
	}

	if err := iae.UpdateState(ReadyDataState); err != nil {
		return fmt.Errorf("source state update failed: %w", err)
	}

	return nil
}

// IsReady checks if the Association's target is ready.
func (a *Association) IsReady() bool {
	if a.target == nil {
		return false
	}

	return a.target.State().Name() == ReadyDataState.Name()
}

// Value returns recalculated IDef's value of the association's target.
func (a *Association) Value(ctx context.Context) (*ItemDefinition, error) {
	if a.target == nil {
		return nil,
			errs.New(
				errs.M("association #%s target isn't defined", a.Id()))
	}

	if err := a.calculate(ctx); err != nil {
		return nil,
			errs.New(
				errs.M("target calculation failed"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("association_id", a.Id()),
				errs.E(err))
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
	srcIds := []string{}
	for k := range a.sources {
		srcIds = append(srcIds, k)
	}

	return srcIds
}

// HasSourceId checks if the Association has source with Id id.
func (a *Association) HasSourceId(id string) bool {
	_, ok := a.sources[id]
	return ok
}

// calculate actualizes target based on current source value.
// if there is no readness of source error isn't occurred and
// associateion target state becomes Unavailable.
// calculate returns error only if transformation or assignment are
// failed.
func (a *Association) calculate(ctx context.Context) error {
	var srcV Value

	if a.transformation == nil {
		if len(a.sources) == 0 {
			return fmt.Errorf("no sources")
		}

		s := a.sources[a.SourcesIds()[0]]

		if s.dataState.name != ReadyDataState.name {
			return fmt.Errorf(
				"source #%s isn't in Ready state (actual state: %s)",
				s.ItemDefinition().Id(), s.dataState.name)
		}

		srcV = s.Value()
	} else {
		var err error

		srcV, err = a.transformation.Evaluate(ctx, a)
		if err != nil {
			return fmt.Errorf("target evaluation failed: %w", err)
		}
	}

	if err := a.target.ItemDefinition().structure.Update(ctx,
		srcV.Get(ctx)); err != nil {
		return fmt.Errorf("target #%s update failed: %w",
			a.target.subject.Id(), err)
	}

	if err := a.target.UpdateState(ReadyDataState); err != nil {
		return fmt.Errorf("target #%s state updating failed: %w",
			a.target.subject.Id(), err)
	}

	return nil
}

// --------------------------- Source interface -------------------------------

// Find looks for source with ItemDefinition's Id equal to name.
// It returns only sources with Ready state.
func (a *Association) Find(_ context.Context, name string) (Data, error) {
	src, ok := a.sources[name]
	if !ok {
		return nil, fmt.Errorf("no source #%s", name)
	}

	if src.dataState.name != ReadyDataState.name {
		return nil,
			fmt.Errorf("source #%s isn't in Ready state (actual state is %s)",
				src.subject.Id(), src.dataState.name)
	}

	return src, nil
}

// -----------------------------------------------------------------------------

// ============================================================================
//                          Assignment
// ============================================================================

// The Assignment class is used to specify a simple mapping of data elements
// using a specified Expression language.
// The default Expression language for all Expressions is specified in the
// Definitions element, using the expressionLanguage attribute. It can also be
// overridden on each individual Assignment using the same attribute.
// type Assignment interface {
// 	foundation.Identifyer
// 	foundation.Documentator
//
// 	Assign(
// 		ctx context.Context,
// 		target *ItemAwareElement,
// 		source Source,
// 	) error
//
// 	// The Expression that evaluates the source of the Assignment.
// 	From FormalExpression
//
// 	// The Expression that defines the actual Assignment operation and the
// 	// target data element.
// 	To FormalExpression
// }
