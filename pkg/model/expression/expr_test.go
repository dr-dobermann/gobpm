package expression

// import (
// 	"bytes"
// 	"testing"

// 	"github.com/dr-dobermann/gobpm/pkg/identity"
// 	"github.com/dr-dobermann/gobpm/pkg/variables"
// 	"github.com/matryer/is"
// )

// func TestExpression(t *testing.T) {
// 	is := is.New(t)

// 	const (
// 		langName = "TEST"
// 		testBody = "x = x + 1; x > y"
// 	)

// 	// create new Expression
// 	fe := New(identity.EmptyID(), langName, variables.Bool)

// 	is.True(fe != nil)
// 	is.True(fe.ID() != identity.EmptyID())
// 	is.True(fe.Language() == langName)
// 	is.True(len(fe.GetBody()) == 0)
// 	is.True(fe.ReturnType() == variables.Bool)

// 	// set body
// 	fe.UpdateBody(bytes.NewBufferString(testBody))
// 	is.True(string(fe.GetBody()) == testBody)
// 	is.True(fe.State() == Created)

// 	testVars := []variables.Variable{
// 		*variables.V("x", variables.Int, 3),
// 		*variables.V("y", variables.Int, 5)}

// 	err := fe.SetParams(testVars...)
// 	is.NoErr(err)
// 	is.True(len(fe.parameters) == 2)
// 	is.True(fe.State() == Parameterized)
// 	params := fe.Params()
// 	for _, p := range params {
// 		found := false
// 		for _, tv := range testVars {
// 			if tv.Name() == p.Name() {
// 				is.True(tv.Type() == p.Type())
// 				is.True(tv.IsEqual(&p))
// 				found = true

// 				break
// 			}
// 		}
// 		if !found {
// 			t.Fatalf("couldn't find '%s' param", p.Name())
// 		}
// 	}
// }

// func TestExprCopy(t *testing.T) {
// 	is := is.New(t)

// 	const (
// 		langName = "TEST"
// 		testBody = "x = x + 1; x > y"
// 	)

// 	fe := New(identity.EmptyID(), langName, variables.Bool)
// 	is.True(fe != nil)

// 	err := fe.SetParams(
// 		*variables.V("x", variables.Int, 3),
// 		*variables.V("y", variables.Int, 5))
// 	is.NoErr(err)
// 	is.True(fe.State() == Parameterized)

// 	sfeStr := fe.Copy()

// 	is.True(sfeStr != nil)
// 	is.True(sfeStr.language == fe.language)
// 	is.True(sfeStr.ID() != fe.ID())
// 	is.True(sfeStr.retType == fe.retType)
// 	is.True(len(sfeStr.parameters) == len(fe.parameters))
// 	is.True(sfeStr.state == Created)

// 	for pn, p := range fe.parameters {
// 		tp, ok := sfeStr.parameters[pn]
// 		if !ok {
// 			t.Fatalf("parameter '%s' not copied", pn)
// 		}
// 		if !p.IsEqual(&tp) {
// 			t.Fatalf("paraneter '%s' copied with errors", pn)
// 		}
// 	}
// }
