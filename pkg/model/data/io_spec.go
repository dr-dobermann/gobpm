package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Activities and Processes often need data in order to execute. In addition
// they can produce data during or as a result of execution. Data requirements
// are captured as Data Inputs and InputSets. Data that is produced is captured
// using Data Outputs and OutputSets. These elements are aggregated in a
// InputOutputSpecification class.
// Certain Activities and CallableElements contain a InputOutputSpecification
// element to describe their data requirements. Execution semantics are defined
// for the InputOutputSpecification and they apply the same way to all elements
// that extend it. Not every Activity type defines inputs and outputs, only
// Tasks, CallableElements (Global Tasks and Processes) MAY define their data
// requirements. Embedded Sub-Processes MUST NOT define Data Inputs and Data
// Outputs directly, however they MAY define them indirectly via
// MultiInstanceLoopCharacteristics.
type InputOutputSpecification struct {
	foundation.BaseElement

	// A reference to the InputSets defined by the InputOutputSpecification.
	// Every InputOutputSpecification MUST define at least one InputSet.
	InputSets []InputSet

	// A reference to the OutputSets defined by the InputOutputSpecification.
	// Every Data Interface MUST define at least one OutputSet.
	OutputSets []OutputSet

	// An optional reference to the Data Inputs of the InputOutputSpecification.
	// If the InputOutputSpecification defines no Data Input, it means no data
	// is REQUIRED to start the Activity. This is an ordered set.
	DataInputs []Input

	// An optional reference to the Data Outputs of the
	// InputOutputSpecification. If the InputOutputSpecification defines no Data
	// Output, it means no data is REQUIRED to finish the Activity. This is an
	// ordered set.
	DataOutputs []Output
}

type SetType uint8

const (
	DefaultSet        SetType = 1 << (iota + 1)
	OptionalSet       SetType = 1 << (iota + 1)
	WhileExecutionSet SetType = 1 << (iota + 1)

	AllSets SetType = DefaultSet | OptionalSet | WhileExecutionSet
)

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
	Name string

	// A DataInput is used in one or more InputSets. This attribute is derived
	// from the InputSets.
	InputSets []*InputSet

	// Each InputSet that uses this DataInput can determine if the Activity can
	// start executing with this DataInput state in “unavailable.” This
	// attribute lists those InputSets.
	InputSetWithOptional []*InputSet

	// Each InputSet that uses this DataInput can determine if the Activity can
	// evaluate this DataInput while executing. This attribute lists those
	// InputSets.
	InputSetWithWhileExecution []*InputSet

	// Defines if the DataInput represents a collection of elements. It is
	// needed when no itemDefinition is referenced. If an itemDefinition is
	// referenced, then this attribute MUST have the same value as the
	// isCollection attribute of the referenced itemDefinition. The default
	// value for this attribute is false.
	IsCollection bool
}

// A Data Output is a declaration that a particular kind of data can be
// produced as output of the InputOutputSpecification. There MAY be multiple
// Data Outputs associated with a InputOutputSpecification.
// The Data Output is an item-aware element. Data Output are visually
// displayed on a top-level Process diagram to show the outputs of the Process
// (i.e., one that is referenced by a Call Activity, where the Call Activity has
// been expanded to show the called Process within the context of a calling
// Process).
type Output struct {
	ItemAwareElement

	// A descriptive name for the element.
	name string

	// A DataOutput is used in one or more outputSets. This attribute is derived
	// from the outputSets.
	outputSets []*OutputSet

	// Each OutputSet that uses this DataOutput can determine if the Activity
	// can complete executing without producing this DataInput. This attribute
	// lists those OutputSets.
	outputSetWithOptional []*OutputSet

	// Each OutputSet that uses this DataInput can determine if the Activity
	// can produce this DataOutput while executing. This attribute lists those
	// OutputSets.
	outputSetWithWhileExecution []*OutputSet

	// Defines if the DataOutput represents a collection of elements. It is
	// needed when no itemDefinition is referenced. If an itemDefinition is
	// referenced, then this attribute MUST have the same value as the
	// isCollection attribute of the referenced itemDefinition. The default
	// value for this attribute is false.
	isCollection bool
}

// NewOutput creates a new Output and returns its pointer on succes or
// error on failure.
func NewOutput(iae *ItemAwareElement, name string) (*Output, error) {
	const defaultName = "Unnamed data output"

	if iae == nil {
		return nil,
			&errs.ApplicationError{
				Message: "ItemAvareElement should be provided for data output",
				Classes: []string{
					errorClass,
					errs.EmptyNotAllowed,
				},
			}
	}

	name = strings.Trim(name, " ")

	if name == "" {
		name = defaultName
	}

	collection := false
	if id := iae.Subject(); id != nil {
		_, ok := id.Structure().(Collection)
		collection = ok
	}

	return &Output{
		ItemAwareElement:            *iae,
		name:                        name,
		outputSets:                  []*OutputSet{},
		outputSetWithOptional:       []*OutputSet{},
		outputSetWithWhileExecution: []*OutputSet{},
		isCollection:                collection,
	}, nil
}

// AddOutputSet adds link to OutputSet(s) in data output
func (o *Output) addOutputSet(os *OutputSet, where SetType) {
	if where&DefaultSet != 0 {
		if ind := index[*OutputSet](os, o.outputSets); ind == -1 {
			o.outputSets = append(o.outputSets, os)
		}
	}

	if where&OptionalSet != 0 {
		if ind := index[*OutputSet](os, o.outputSetWithOptional); ind == -1 {
			o.outputSetWithOptional = append(o.outputSetWithOptional, os)
		}
	}

	if where&WhileExecutionSet != 0 {
		if ind := index[*OutputSet](os,
			o.outputSetWithWhileExecution); ind == -1 {
			o.outputSetWithWhileExecution = append(
				o.outputSetWithWhileExecution, os)
		}
	}
}

// removeOutputSet removes outputSet reference(s) from data Output.
func (o *Output) removeOutputSet(os *OutputSet, from SetType) {
	if from&DefaultSet != 0 {
		if ind := index[*OutputSet](os, o.outputSets); ind != -1 {
			o.outputSets = append(o.outputSets[:ind], o.outputSets[ind+1:]...)
		}
	}

	if from&OptionalSet != 0 {
		if ind := index[*OutputSet](os, o.outputSetWithOptional); ind != -1 {
			o.outputSetWithOptional = append(o.outputSetWithOptional[:ind],
				o.outputSetWithOptional[ind+1:]...)
		}
	}

	if from&WhileExecutionSet != 0 {
		if ind := index[*OutputSet](os, o.outputSetWithWhileExecution); ind != -1 {
			o.outputSetWithWhileExecution = append(
				o.outputSetWithWhileExecution[:ind],
				o.outputSetWithWhileExecution[ind+1:]...)
		}
	}
}

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

// An OutputSet is a collection of DataOutputs elements that together can be
// produced as output from an Activity or Event. An InputOutputSpecification
// element MUST define at least OutputSet element. An OutputSet MAY reference
// zero or more DataOutput elements. A single DataOutput MAY be associated with
// multiple OutputSet elements, but it MUST always be referenced by at least one
// OutputSet.
// An “empty” OutputSet, one that is associated with no DataOutput elements,
// signifies that the ACTIVITY produces no data.
type OutputSet struct {
	foundation.BaseElement

	// A descriptive name for the input set.
	name string

	// The DataOutput elements that MAY collectively be outputted.
	dataOutputs []*Output

	// The DataOutput elements that are a part of the OutputSet that do not
	// have to be produced when the Activity completes executing. This
	// association MUST NOT reference a DataOutput that is not listed in the
	// dataOutputRefs.
	optionalOutputs []*Output

	// The DataOutput elements that are a part of the OutputSet that can be
	// produced while the Activity is executing. This association MUST NOT
	// reference a DataOutput that is not listed in the dataOutputRefs.
	whileExecutionOutputs []*Output

	// Specifies an Input/Output rule that defines which InputSet has to
	// become valid to expect the creation of this OutputSet. This attribute is
	// paired with the outputSetRefs attribute of InputSets. This combination
	// replaces the IORules attribute for Activities in BPMN 1.2.
	inputSets []*InputSet
}

// NewOutputSet creates a new OutputSet and returns its pointer on succes or
// error on failure.
func NewOutputSet(
	name string,
	baseOpts ...options.Option,
) (*OutputSet, error) {
	name = trim(name)
	if err := checkStr(name, "output set should have a name"); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't create an output set",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &OutputSet{
		BaseElement:           *be,
		name:                  name,
		dataOutputs:           []*Output{},
		optionalOutputs:       []*Output{},
		whileExecutionOutputs: []*Output{},
		inputSets:             []*InputSet{},
	}, nil
}

// AddOutput adds output at one or few OutputSets if its not found in them yet.
func (os *OutputSet) AddOutput(o *Output, where SetType) {
	if o == nil {
		return
	}

	if where&DefaultSet != 0 {
		ind := index[*Output](o, os.dataOutputs)
		if ind == -1 {
			os.dataOutputs = append(os.dataOutputs, o)

			o.addOutputSet(os, DefaultSet)
		}
	}

	if where&OptionalSet != 0 {
		ind := index[*Output](o, os.optionalOutputs)
		if ind == -1 {
			os.optionalOutputs = append(os.optionalOutputs, o)

			o.addOutputSet(os, OptionalSet)
		}
	}

	if where&WhileExecutionSet != 0 {
		ind := index[*Output](o, os.whileExecutionOutputs)
		if ind == -1 {
			os.whileExecutionOutputs = append(os.whileExecutionOutputs, o)

			o.addOutputSet(os, WhileExecutionSet)
		}
	}
}

// RemoveOutput removes singe ouptut from one or few OutputSets.
func (os *OutputSet) RemoveOutput(o *Output, from SetType) {
	if o == nil {
		return
	}

	if from&DefaultSet != 0 {
		if ind := index[*Output](o, os.dataOutputs); ind != -1 {
			os.dataOutputs = append(os.dataOutputs[:ind],
				os.dataOutputs[ind+1:]...)

			o.removeOutputSet(os, DefaultSet)
		}
	}

	if from&OptionalSet != 0 {
		if ind := index[*Output](o, os.optionalOutputs); ind != -1 {
			os.optionalOutputs = append(os.optionalOutputs[:ind],
				os.optionalOutputs[ind+1:]...)

			o.removeOutputSet(os, OptionalSet)
		}
	}

	if from&WhileExecutionSet != 0 {
		if ind := index[*Output](o, os.whileExecutionOutputs); ind != -1 {
			os.whileExecutionOutputs = append(os.whileExecutionOutputs[:ind],
				os.whileExecutionOutputs[ind+1:]...)

			o.removeOutputSet(os, WhileExecutionSet)
		}
	}
}
