package gep_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	"github.com/dr-dobermann/gobpm/pkg/expression/gep/operations"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/matryer/is"
)

// testing evaluation of x = x + y expression
func TestGEPAdd(t *testing.T) {
	is := is.New(t)

	g := gep.New(mid.EmptyID(), vars.Int)
	is.True(g != nil)
	is.True(g.ReturnType() == vars.Int)
	is.True(g.ID() != mid.EmptyID())

	// try to evaluate expression with an empty opertion list
	is.True(g.Evaluate() != nil)

	xVal := 18
	yVal := 5
	y := vars.V("y", vars.Int, yVal)

	err := g.AddOperation(
		gep.Operation{
			Func:     operations.Add(y, "x"),
			OpLoader: gep.GetVar(vars.V("x", vars.Int, xVal))})
	is.NoErr(err)

	// adding empty operation should return non-nil error
	emptyOp := gep.Operation{}
	is.True(g.AddOperation(emptyOp) != nil)

	err = g.Evaluate()
	is.NoErr(err)

	res, err := g.GetResult()
	is.NoErr(err)
	is.True(res.I == 23)
}

func TestGEPExpressionInterface(t *testing.T) {
	// g := new(gep.GEP)
	// _, ok := g.(expression.Expression)
	// if !ok {
	// 	t.Fatal("GEP is not implemented experssion.Expression interface")
	// }
}
