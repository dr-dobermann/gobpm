package gateways

import "github.com/dr-dobermann/gobpm/pkg/model/flow"

const errorClass = "GATEWAYS_ERRORS"

var (
	_ flow.Node = (*Gateway)(nil)

	_ flow.SequenceSource = (*Gateway)(nil)
	_ flow.SequenceTarget = (*Gateway)(nil)
)
