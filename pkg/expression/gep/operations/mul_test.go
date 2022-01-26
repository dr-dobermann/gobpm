package operations

import (
	"fmt"
	"testing"
	"time"

	vars "github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/matryer/is"
)

var mulTests = []testCase{
	{vars.Int, 5, vars.Int, 7, "x", vars.VariableValues{I: 35}, shouldPass},
	{vars.Int, 5, vars.Bool, false, "x", vars.VariableValues{I: -1}, shouldFail},
	{vars.Int, 5, vars.String, "10", "x", vars.VariableValues{I: 50}, shouldPass},
	{vars.Int, 5, vars.String, "trash", "x", vars.VariableValues{I: -1}, shouldFail},
	{vars.Int, 5, vars.Float, -7.9, "x", vars.VariableValues{I: -40}, shouldPass},
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

	{vars.Float, 7.7, vars.Int, 7, "x", vars.VariableValues{F: 7.7 * 7.0}, shouldPass},
	{vars.Float, 7.7, vars.Bool, false, "x", vars.VariableValues{F: -1}, shouldFail},
	{vars.Float, 7.7, vars.String, "10", "x", vars.VariableValues{F: 7.7 * 10.0}, shouldPass},
	{vars.Float, 7.7, vars.String, "trash", "x", vars.VariableValues{F: -1}, shouldFail},
	{vars.Float, 7.7, vars.Float, 7.9, "x", vars.VariableValues{F: 7.9 * 7.7}, shouldPass},
	{vars.Float, 7.7, vars.Time, time.Now(), "x", vars.VariableValues{F: -1}, shouldFail},

	{vars.Time, time.Now(), vars.Int, 7, "x", vars.VariableValues{T: time.Time{}}, shouldFail},
	{vars.Time, time.Now(), vars.Bool, true, "x", vars.VariableValues{T: time.Time{}}, shouldFail},
	{vars.Time, time.Now(), vars.String, "trash", "x", vars.VariableValues{T: time.Time{}}, shouldFail},
	{vars.Time, time.Now(), vars.Float, 7.3, "x", vars.VariableValues{T: time.Time{}}, shouldFail},
	{vars.Time, time.Now(), vars.Time, time.Now(), "x", vars.VariableValues{T: time.Time{}}, shouldFail},
}

func TestMul(t *testing.T) {
	is := is.New(t)

	for _, tc := range mulTests {
		testName := fmt.Sprintf("%v(%v) * %v(%v)", tc.xt, tc.xv, tc.yt, tc.yv)

		t.Run(testName, func(t *testing.T) {
			x := vars.V(tc.xt.String(), tc.xt, tc.xv)
			y := vars.V(tc.yt.String(), tc.yt, tc.yv)

			mulOp, err := Mul(y, "x")
			is.NoErr(err)
			is.True(mulOp != nil)

			res, err := mulOp(x)
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