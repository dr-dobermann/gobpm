//nolint:dupl
package data

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ************************ Input **********************************************

// A Data Input is a declaration that a particular kind of data will be used as
// input of the InputOutputSpecification. There may be multiple Data Inputs
// associated with an InputOutputSpecification.
// The Data Input is an item-aware element. Data Inputs are visually displayed
// on a Process diagram to show the inputs to the top-level Process or to show
// the inputs of a called Process (i.e., one that is referenced by a Call
// Activity, where the Call Activity has been expanded to show the called
// Process within the context of a calling Process).
type Input struct {
	ItemAwareElement

	// A descriptive name for the element.
	name string

	// A DataInput is used in one or more InputSets. This attribute is derived
	// from the InputSets.
	inputSets []*InputSet

	// Each InputSet that uses this DataInput can determine if the Activity can
	// start executing with this DataInput state in “unavailable.” This
	// attribute lists those InputSets.
	inputSetWithOptional []*InputSet

	// Each InputSet that uses this DataInput can determine if the Activity can
	// evaluate this DataInput while executing. This attribute lists those
	// InputSets.
	inputSetWithWhileExecution []*InputSet

	// Defines if the DataInput represents a collection of elements. It is
	// needed when no itemDefinition is referenced. If an itemDefinition is
	// referenced, then this attribute MUST have the same value as the
	// isCollection attribute of the referenced itemDefinition. The default
	// value for this attribute is false.
	isCollection bool
}

// NewInput creates a new Input and returns its pointer on success
// or error on failure.
func NewInput(iae *ItemAwareElement, name string) (*Input, error) {
	const defaultName = "Unnamed data input"

	if iae == nil {
		return nil,
			&errs.ApplicationError{
				Message: "ItemAvareElement should be provided for data input",
				Classes: []string{
					errorClass,
					errs.EmptyNotAllowed,
				},
			}
	}

	name = trim(name)

	if name == "" {
		name = defaultName
	}

	collection := false
	if id := iae.Subject(); id != nil {
		_, ok := id.Structure().(Collection)
		collection = ok
	}

	return &Input{
		ItemAwareElement:           *iae,
		name:                       name,
		inputSets:                  []*InputSet{},
		inputSetWithOptional:       []*InputSet{},
		inputSetWithWhileExecution: []*InputSet{},
		isCollection:               collection,
	}, nil
}

// Name returns a name of the Input.
func (in *Input) Name() string {
	return in.name
}

// addInputSet add an InputSet to the Input.
func (in *Input) addInputSet(is *InputSet, where SetType) {
	if where&DefaultSet != 0 {
		if ind := index[*InputSet](is, in.inputSets); ind == -1 {
			in.inputSets = append(in.inputSets, is)
		}
	}

	if where&OptionalSet != 0 {
		if ind := index[*InputSet](is, in.inputSetWithOptional); ind == -1 {
			in.inputSetWithOptional = append(in.inputSetWithOptional, is)
		}
	}

	if where&WhileExecutionSet != 0 {
		if ind := index[*InputSet](
			is,
			in.inputSetWithWhileExecution); ind == -1 {
			in.inputSetWithWhileExecution = append(
				in.inputSetWithWhileExecution, is)
		}
	}
}

// removeInputSet removes the Inputset from the Input.
func (in *Input) removeInputSet(is *InputSet, from SetType) {
	if from&DefaultSet != 0 {
		if ind := index[*InputSet](is, in.inputSets); ind != -1 {
			in.inputSets = append(
				in.inputSets[:ind],
				in.inputSets[ind+1:]...)
		}
	}

	if from&OptionalSet != 0 {
		if ind := index[*InputSet](is, in.inputSetWithOptional); ind != -1 {
			in.inputSetWithOptional = append(
				in.inputSetWithOptional[:ind],
				in.inputSetWithOptional[ind+1:]...)
		}
	}

	if from&WhileExecutionSet != 0 {
		if ind := index[*InputSet](
			is,
			in.inputSetWithWhileExecution); ind != -1 {
			in.inputSetWithWhileExecution = append(
				in.inputSetWithWhileExecution[:ind],
				in.inputSetWithWhileExecution[ind+1:]...)
		}
	}
}

// ********************* InputSet **********************************************

// An InputSet is a collection of DataInput elements that together define a
// valid set of data inputs for an InputOutputSpecification. An
// InputOutputSpecification MUST have at least one InputSet element. An InputSet
// MAY reference zero or more DataInput elements. A single DataInput MAY be
// associated with multiple InputSet elements, but it MUST always be referenced
// by at least one InputSet.
// An “empty” InputSet, one that references no DataInput elements, signifies
// that the Activity requires no data to start executing (this implies that
// either there are no data inputs or they are referenced by another input set).
// InputSet elements are contained by InputOutputSpecification elements; the
// order in which these elements are included defines the order in which they
// will be evaluated.
type InputSet struct {
	foundation.BaseElement

	// A descriptive name for the input set.
	name string

	// The DataInput elements that collectively make up this data requirement.
	dataInputs []*Input

	// The DataInput elements that are a part of the InputSet that can be in
	// the state of “unavailable” when the Activity starts executing. This
	// association MUST NOT reference a DataInput that is not listed in the
	// dataInputRefs.
	optionalInputs []*Input

	// The DataInput elements that are a part of the InputSet that can be
	// evaluated while the Activity is executing. This association MUST NOT
	// reference a DataInput that is not listed in the dataInputRefs.
	whileExecutionInputs []*Input

	// Specifies an Input/Output rule that defines which OutputSet is expected
	// to be created by the Activity when this InputSet became valid.
	// This attribute is paired with the inputSetRefs attribute of OutputSets.
	// This combination replaces the IORules attribute for Activities in
	// BPMN 1.2.
	outputSets []*OutputSet
}

// NewInputSet creates a new InputSet and returns its pointer on success or
// error on failure.
func NewInputSet(
	name string,
	baseOpts ...options.Option,
) (*InputSet, error) {
	name = trim(name)
	if err := checkStr(name, "input set should have a name"); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't create an input set",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &InputSet{
		BaseElement:          *be,
		name:                 name,
		dataInputs:           []*Input{},
		optionalInputs:       []*Input{},
		whileExecutionInputs: []*Input{},
		outputSets:           []*OutputSet{},
	}, err
}

// Name returns the name of the InptutSet.
func (is *InputSet) Name() string {
	return is.name
}

// AddInput adds the Input to the InputSet.
func (is *InputSet) AddInput(in *Input, where SetType) {
	if in == nil {
		return
	}

	if where&DefaultSet != 0 {
		ind := index[*Input](in, is.dataInputs)
		if ind == -1 {
			is.dataInputs = append(is.dataInputs, in)

			in.addInputSet(is, DefaultSet)
		}
	}

	if where&OptionalSet != 0 {
		ind := index[*Input](in, is.optionalInputs)
		if ind == -1 {
			is.optionalInputs = append(is.optionalInputs, in)

			in.addInputSet(is, OptionalSet)
		}
	}

	if where&WhileExecutionSet != 0 {
		ind := index[*Input](in, is.whileExecutionInputs)
		if ind == -1 {
			is.whileExecutionInputs = append(is.whileExecutionInputs, in)

			in.addInputSet(is, WhileExecutionSet)
		}
	}
}

// RemoveInput removes the Input from the InputSet.
func (is *InputSet) RemoveInput(in *Input, from SetType) {
	if in == nil {
		return
	}

	if from&DefaultSet != 0 {
		if ind := index[*Input](in, is.dataInputs); ind != -1 {
			is.dataInputs = append(is.dataInputs[:ind],
				is.dataInputs[ind+1:]...)

			in.removeInputSet(is, DefaultSet)
		}
	}

	if from&OptionalSet != 0 {
		if ind := index[*Input](in, is.optionalInputs); ind != -1 {
			is.optionalInputs = append(is.optionalInputs[:ind],
				is.optionalInputs[ind+1:]...)

			in.removeInputSet(is, OptionalSet)
		}
	}

	if from&WhileExecutionSet != 0 {
		if ind := index[*Input](in, is.whileExecutionInputs); ind != -1 {
			is.whileExecutionInputs = append(is.whileExecutionInputs[:ind],
				is.whileExecutionInputs[ind+1:]...)

			in.removeInputSet(is, WhileExecutionSet)
		}
	}
}
