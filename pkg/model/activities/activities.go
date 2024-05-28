package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "ACTIVITIES_ERRORS"

// interfaces check
var (
	_ flow.Node = (*Activity)(nil)

	_ flow.SequenceSource = (*Activity)(nil)
	_ flow.SequenceTarget = (*Activity)(nil)
)
