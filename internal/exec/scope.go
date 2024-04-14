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
// executing subprocess and tasks add to the path as subprocces/id/tasks/id on
// their start and removes them on their finish.
// full data path could be as '/subprocesses/subp1/tasks/task1'
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

type Scope interface {
	// Scope Name consists of
	foundation.Namer

	// GetData tries to return value of data.Data object with name Name
	// which should be at path.
	GetData(path DataPath, name string) (data.Value, error)
}
