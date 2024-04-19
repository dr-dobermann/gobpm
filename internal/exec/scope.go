// Scope is the storage of available Data objects.
//
// Inititial scope created by Instance of the process. It fills with
// process Poperties, Input data.Parameters and DataObjects.
//
// When Node starts execution:

//   - it creates a new child Scope and fills it with its Properties.
//   - it checks all input data.Parameters and if there is no any, it fails with
//     outlined process.
//   - after successful finish:
//     - it stores output data.Parameters in outer Scope.
//     - fills all outgoing data.Associations.
//   - closes created child Scope.

package exec

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/helpers"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// DataPath is path to data in the scope.
// root path '/' holds process'es Properties and DataObjects.
// executing subprocess and tasks add to the path as next layer on
// their start and removes them on their finish.
// full data path could be as '/subprocess_name/task_name'
type DataPath string

const (
	EmptyDataPath DataPath = ""
	RootDataPath  DataPath = "/"

	PathSeparator string = "/"
)

// New creates a new DataPath from correctly formed string.
// If there is errors in newPath, EmptyDataPath and error returned.
func NewDataPath(newPath string) (DataPath, error) {
	d := DataPath(newPath)
	if err := d.Validate(); err != nil {
		return EmptyDataPath, err
	}

	return d, nil
}

// Validate checks the DataPath and if it has any error, it return error.
func (p DataPath) Validate() error {
	invPath := errs.New(
		errs.M("invalid data path (should start from /): %q", p),
		errs.C(errorClass, errs.InvalidParameter))

	fields := strings.Split(helpers.Strim(string(p)), "/")

	if len(fields) == 0 {
		return invPath
	}

	// first element is empty if path starts from '/'
	if fields[0] != "" {
		return invPath
	}

	// fields doesn't have empty or untrimmed values
	for i := 1; i < len(fields); i++ {
		if helpers.Strim(fields[i]) == "" {
			return invPath
		}
	}

	return nil
}

func (p DataPath) String() string {
	return string(p)
}

// DropTail drops last part of the path and returns DataPath consisted the
// rest of the p.
func (p DataPath) DropTail() (DataPath, error) {
	pRef := p.String()
	pp := strings.Split(pRef, PathSeparator)
	if len(pp) < 2 {
		return EmptyDataPath, errs.New(
			errs.M("path is too short to drop its tail: " + p.String()))
	}

	return NewDataPath(strings.Join(pp[:len(pp)-1], PathSeparator))
}

// Append adds non-empyt tail to the DataPath p and retruns new DataPath on success
// or error on failure
func (p DataPath) Append(tail string) (DataPath, error) {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return EmptyDataPath,
			errs.New(
				errs.M("couldn't add empty string to tail"))
	}

	return NewDataPath(string(p) + PathSeparator + tail)
}

// Scope keeps all variables of the scope and returns its values.
type Scope interface {
	// Root returns the root dataPath of the Scope.
	Root() DataPath

	// Scopes returns list of scopes controlled by Scope.
	Scopes() []DataPath

	// LoadData loads a data data.Data into the Scope into
	// the DataPath.
	LoadData(NodeDataLoader, ...data.Data) error

	// GetData tries to return value of data.Data object with name Name.
	// dataPath selects the initial scope to look for the name.
	// If current Scope doesn't find the name, then it looks in upper
	// Scope until find or failed to find.
	GetData(dataPath DataPath, name string) (data.Value, error)

	// AddData adds data.Data to the NodeDataLoader scope or to rootScope
	// if NodeDataLoader is nil.
	AddData(NodeDataLoader, ...data.Data) error

	// ExtendScope adds a new child Scope to the Scope and returns
	// its full path.
	ExtendScope(NodeDataLoader) error

	// LeaveScope calls the Scope to clear all data saved by NodeDataLoader.
	LeaveScope(NodeDataLoader) error
}

// NodeDataLoader is implemented by those nodes, which stores data while
// its execution.
type NodeDataLoader interface {
	// Name returns NodeDataLoader name to create a scope name.
	flow.Node

	// RegisterData sends all Node Data to the scope
	RegisterData(DataPath, Scope) error
}
