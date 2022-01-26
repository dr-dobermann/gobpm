package operations

import (
	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

var (
	functions = map[string]map[vars.Type]gep.FunctionDefinition{
		mulFunction: mulFunctions,
		subFunction: subFunctions,
		addFunction: addFunctions,
		divFunction: divFunctions,
	}
)

func init() {
	// register function definitions
	for fName, fdm := range functions {
		for t, fd := range fdm {
			if err := gep.AddOpFuncDefinition(fName, t, fd); err != nil {
				panic(err.Error())
			}
		}
	}
}
