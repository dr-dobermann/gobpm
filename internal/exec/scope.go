package exec

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type DataPath string

type Scope interface {
	foundation.Namer

	GetData(DataPath) (data.Value, error)
}
