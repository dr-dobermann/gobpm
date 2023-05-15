package expression

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/identity"
)

//nolint: revive
type ExpressionError struct {
	exprID identity.Id
	msg    string
	Err    error
}

func (ee ExpressionError) Error() string {
	return fmt.Sprintf("Expression #%s error %q: %v",
		ee.exprID.String(), ee.msg, ee.Err)
}
