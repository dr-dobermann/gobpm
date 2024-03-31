package exec

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// DataPath is path to data in the scope.
// root path '/' holds process'es Properties and DataObjects.
// executing subprocess and tasks add to the path as subprocces/id/tasks/id on
// their start and removes them on their finish.
// full data path could be as '/subprocesses/subp1/tasks/task1'
type DataPath string

type Scope interface {
	// Scope Name consists of
	foundation.Namer

	// GetData tries to return value of data.Data object with name Name
	// which should be at path.
	GetData(path DataPath, nmae string) (data.Value, error)
}
