// Scope is the storage of available Data objects.
//
// Inititial scope created by Instance of the process. It fills with
// process Poperties, Input data.Parameters and DataObjects.
//
// When Node starts execution:

//   - it creates a new child Scope and fills it with its Properties.
//   - it checks all input data.Parameters and if there is no any, it fails with
//     outlined process.
//   - after successfull finish:
//     - it stores output data.Parameters in outer Scope.
//     - fills all outgoing data.Associations.
//   - closes created child Scope.

package exec

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/helpers"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// DataPath is path to data in the scope.
// root path '/' holds process'es Properties and DataObjects.
// executing subprocess and tasks add to the path as next layer on
// their start and removes them on their finish.
// full data path could be as '/subprocess_name/task_name'
type DataPath string

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

// Scope keeps all variables of the scope and returns its values.
type Scope interface {
	// Scope Name consists of
	foundation.Namer

	// GetData tries to return value of data.Data object with name Name.
	GetData(path DataPath, name string) (data.Value, error)
}

// DataProvider is implemented by those nodes, which stores data while
// its execution.
type DataProvider interface {
	RegisterData(RuntimeEnvironment) error
	LeaveScope(sopeName string) error
}
