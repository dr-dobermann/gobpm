package extension

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	"github.com/dr-dobermann/gobpm/pkg/variables"
)

// list comparator
// this function compares the variable with a given list
// and if it's equal, the result is the index of the elemnt in the list
// if there is no element which is equal to the variable, then -1 returned.
//
// Variables in the list should have the same type as a tested one or
// be convertable to it.
func CheckIfExists(
	ctx context.Context,
	resName string,
	vl ...*variables.Variable) (gep.OpFunc, error) {

	inCh := make(chan variables.Variable)
	stopCh := make(chan struct{})

	// send all variable into the channel
	go func() {
		defer close(inCh)

		for _, v := range vl {
			v := v
			select {
			case <-ctx.Done():
				return

			case inCh <- *v:

			case <-stopCh:
				return
			}
		}
	}()

	return func(x *variables.Variable) (*variables.Variable, error) {
		i := 0
		for y := range inCh {
			if x.IsEqual(&y) {
				close(stopCh)
				return variables.V(resName, variables.Int, i), nil
			}
			i++
		}

		return variables.V(resName, variables.Int, -1), nil
	}, nil
}
