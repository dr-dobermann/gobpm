package operations

import (
	"fmt"
	"testing"
	"time"

	vars "github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/matryer/is"
)

var divTests = []testCase{
	{vars.Int, 8, vars.Int, 2, "x", vars.VariableValues{I: 4}, shouldPass},
	{vars.Int, 8, vars.Int, 0, "x", vars.VariableValues{I: 4}, shouldFail},
	{vars.Int, 5, vars.Bool, false, "x", vars.VariableValues{I: -1}, shouldFail},
	{vars.Int, 10, vars.String, "2", "x", vars.VariableValues{I: 5}, shouldPass},
	{vars.Int, 5, vars.String, "trash", "x", vars.VariableValues{I: -1}, shouldFail},
	{vars.Int, 50, vars.Float, -10.0, "x", vars.VariableValues{I: -5}, shouldPass},
	{vars.Int, 50, vars.Float, 0, "x", vars.VariableValues{I: -5}, shouldFail},
	{vars.Int, 5, vars.Time, time.Now(), "x", vars.VariableValues{I: -1}, shouldFail},

	{vars.Bool, false, vars.Int, 7, "x", vars.VariableValues{B: false}, shouldFail},
	{vars.Bool, false, vars.Bool, true, "x", vars.VariableValues{B: false}, shouldFail},
	{vars.Bool, false, vars.String, "trash", "x", vars.VariableValues{B: false}, shouldFail},
	{vars.Bool, false, vars.Float, 7.3, "x", vars.VariableValues{B: false}, shouldFail},
	{vars.Bool, false, vars.Time, time.Now(), "x", vars.VariableValues{B: false}, shouldFail},

	{vars.String, "trash", vars.Int, 7, "x", vars.VariableValues{S: "trash"}, shouldFail},
	{vars.String, "trash", vars.Bool, true, "x", vars.VariableValues{S: "trash"}, shouldFail},
	{vars.String, "trash", vars.String, "trash", "x", vars.VariableValues{S: "trash"}, shouldFail},
	{vars.String, "trash", vars.Float, 7.3, "x", vars.VariableValues{S: "trash"}, shouldFail},
	{vars.String, "trash", vars.Time, time.Now(), "x", vars.VariableValues{S: "trash"}, shouldFail},

	{vars.Float, 7.7, vars.Int, 11, "x", vars.VariableValues{F: 7.7 / 11}, shouldPass},
	{vars.Float, 7.7, vars.Int, 0, "x", vars.VariableValues{F: 7.7 / 11}, shouldFail},
	{vars.Float, 7.7, vars.Bool, false, "x", vars.VariableValues{F: -1}, shouldFail},
	{vars.Float, 7.7, vars.String, "10", "x", vars.VariableValues{F: 7.7 / 10.0}, shouldPass},
	{vars.Float, 7.7, vars.String, "trash", "x", vars.VariableValues{F: -1}, shouldFail},
	{vars.Float, 7.7, vars.Float, 7.9, "x", vars.VariableValues{F: 7.7 / 7.9}, shouldPass},
	{vars.Float, 7.7, vars.Float, 0.0, "x", vars.VariableValues{F: 7.7 / 7.9}, shouldFail},
	{vars.Float, 7.7, vars.Time, time.Now(), "x", vars.VariableValues{F: -1}, shouldFail},

	{vars.Time, time.Now(), vars.Int, 7, "x", vars.VariableValues{T: time.Time{}}, shouldFail},
	{vars.Time, time.Now(), vars.Bool, true, "x", vars.VariableValues{T: time.Time{}}, shouldFail},
	{vars.Time, time.Now(), vars.String, "trash", "x", vars.VariableValues{T: time.Time{}}, shouldFail},
	{vars.Time, time.Now(), vars.Float, 7.3, "x", vars.VariableValues{T: time.Time{}}, shouldFail},
	{vars.Time, time.Now(), vars.Time, time.Now(), "x", vars.VariableValues{T: time.Time{}}, shouldFail},
}

func TestDiv(t *testing.T) {
	is := is.New(t)

	for _, tc := range divTests {
		testName := fmt.Sprintf("%v(%v) * %v(%v)", tc.xt, tc.xv, tc.yt, tc.yv)

		t.Run(testName, func(t *testing.T) {
			x := vars.V(tc.xt.String(), tc.xt, tc.xv)
			y := vars.V(tc.yt.String(), tc.yt, tc.yv)

			divOp, err := Div(y, "x")
			is.NoErr(err)
			is.True(divOp != nil)

			res, err := divOp(x)
			if err != nil {
				if tc.testType == shouldFail {
					return
				}

				t.Fatal("test should pass")
			}

			if tc.testType == shouldFail {
				t.Fatal("test should fail")
			}

			is.True(res != nil)

			is.True(checkRes(tc.xt, tc.resValue, *res))
		})
	}
}
