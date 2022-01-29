package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/dr-dobermann/gobpm/examples/gep/extension"
	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/variables"
)

func main() {
	calcSqrt()
	checkList()
}

func calcSqrt() {
	g := gep.New(identity.EmptyID(), variables.Float)

	sqrt, err := extension.MathFuncCaller(
		math.Sqrt, "sqrt",
		gep.ParamTypeChecker(variables.Float, "sqrt"),
		extension.CheckPositive)

	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't get sqrt opFunc: %v", err)
		return
	}

	err = g.AddOperation(gep.Operation{
		Func:     sqrt,
		ParamLdr: gep.LoadVar(variables.V("x", variables.Float, 4.0))})
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't add sqrt opFunc: %v", err)
		return
	}

	if err = g.Evaluate(); err != nil {
		fmt.Fprintf(os.Stderr, "evaluation error: %v", err)
		return
	}

	res, err := g.GetResult()
	if err != nil {
		fmt.Fprintf(os.Stderr, "evaluation error: %v", err)
		return
	}

	fmt.Printf("result (%s[%v]): %f\n", res.Name(), res.Type(), res.Float64())
}

func checkList() {
	g := gep.New(identity.EmptyID(), variables.Int)

	data := []float64{12, 1, 4.5, 5, 0.35, 6.4, 6.2, 7.2}
	vars := []*variables.Variable{}
	for i, d := range data {
		vars = append(vars, variables.V(strconv.Itoa(i), variables.Float, d))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	finder, err := extension.CheckIfExists(ctx, "idx", vars...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't create sqrt opFunc: %v", err)
		return
	}

	err = g.AddOperation(gep.Operation{
		Func:     finder,
		ParamLdr: gep.LoadVar(variables.V("item", variables.Float, data[4]))})
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't add  opFunc: %v", err)
		return
	}

	if err := g.Evaluate(); err != nil {
		fmt.Fprintf(os.Stderr, "evaluation error: %v", err)
		return
	}

	res, err := g.GetResult()
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't get expression results: %v", err)
		return
	}

	fmt.Printf("index %s{%v}: %d\n", res.Name(), res.Type(), res.Int())
}
