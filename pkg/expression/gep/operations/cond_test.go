package operations

import (
	"fmt"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/expression/gep"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/matryer/is"
)

func TestConditions(t *testing.T) {
	for _, tn := range []string{"equal", "notEqual", "less", "greater", "le", "ge"} {
		testCondition(t, tn)
	}
}

func testCondition(t *testing.T, condName string) {
	is := is.New(t)

	testDescr, ok := tests[condName]
	is.True(ok)

	for _, tc := range testDescr.testCases {
		testName := fmt.Sprintf("%s: %v(%v) %s %v(%v)",
			condName, tc.xt, tc.xv, testDescr.opStr, tc.yt, tc.yv)

		t.Run(testName, func(t *testing.T) {
			x := vars.V(tc.xt.String(), tc.xt, tc.xv)
			y := vars.V(tc.yt.String(), tc.yt, tc.yv)

			condOp, err := testDescr.condFunc(y, "res")
			is.NoErr(err)
			is.True(condOp != nil)

			res, err := condOp(x)
			if err != nil {
				if tc.testType == shouldFail {
					return
				}

				t.Fatal("test should pass, but have error:", err)
			}

			if tc.testType == shouldFail {
				t.Fatal("test should fail")
			}

			is.True(res != nil)

			if !checkRes(tc.xt, tc.resValue, *res) {
				t.Fatalf("result %v doesn't meet expectation %v", res.VariableValues, tc.resName)
			}
		})
	}
}

var (
	strTime = "1973-02-23T05:15:00+06:00"

	timeTest, _ = time.Parse(time.RFC3339, strTime)

	equalTests = []testCase{
		{vars.Int, 5, vars.Int, 5, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Int, 5, vars.Int, 7, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Int, 5, vars.Bool, false, "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Int, 5, vars.String, "5", "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Int, 5, vars.String, "7", "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Int, 5, vars.String, "trash", "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Int, 5, vars.Float, -7.9, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Int, 5, vars.Float, 4.9, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Int, timeTest.UnixMilli(), vars.Time, timeTest, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Int, 5, vars.Time, time.Now(), "res", vars.VariableValues{B: false}, shouldPass},

		{vars.Bool, true, vars.Int, 5, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, true, vars.Int, 0, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Bool, true, vars.Bool, true, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, true, vars.Bool, false, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Bool, true, vars.String, "true", "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, true, vars.String, "trash", "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Bool, true, vars.Float, -7.9, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, true, vars.Float, 0.0, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Bool, true, vars.Time, time.Now(), "res", vars.VariableValues{I: -1}, shouldFail},

		{vars.String, "10", vars.Int, 10, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "test", vars.Int, 0, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, "true", vars.Bool, true, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "test", vars.Bool, false, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, "good", vars.String, "good", "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "bad", vars.String, "trash", "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, "-7.90", vars.Float, -7.9, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "trash", vars.Float, 0.0, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, strTime, vars.Time, timeTest, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "trash", vars.Time, timeTest, "res", vars.VariableValues{B: false}, shouldPass},

		{vars.Float, 10.0, vars.Int, 10, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Float, 15.0, vars.Int, 7, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Float, 3, vars.Bool, true, "res", vars.VariableValues{B: true}, shouldFail},
		{vars.Float, 12.53, vars.String, "12.53", "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Float, 12.06, vars.String, "12.53", "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Float, 7, vars.String, "trash", "res", vars.VariableValues{B: false}, shouldFail},
		{vars.Float, -7.90, vars.Float, -7.9, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Float, 12, vars.Float, 0.0, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Float, 3, vars.Time, timeTest, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Float, float64(timeTest.UnixMilli()), vars.Time, timeTest, "res", vars.VariableValues{B: false}, shouldPass},

		{vars.Time, timeTest, vars.Int, 10, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Time, timeTest, vars.Int, timeTest.UnixMilli(), "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Time, timeTest, vars.Bool, true, "res", vars.VariableValues{B: true}, shouldFail},
		{vars.Time, timeTest, vars.String, strTime, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Time, timeTest, vars.String, "trash", "res", vars.VariableValues{B: true}, shouldFail},
		{vars.Time, timeTest, vars.Float, -7.9, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Time, timeTest, vars.Float, float64(timeTest.UnixMilli()), "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Time, timeTest, vars.Time, timeTest, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Time, timeTest, vars.Time, time.Now(), "res", vars.VariableValues{B: false}, shouldPass},
	}

	notEqualTests = []testCase{
		{vars.Int, 5, vars.Int, 5, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Int, 5, vars.Int, 7, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Int, 5, vars.Bool, false, "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Int, 5, vars.String, "5", "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Int, 5, vars.String, "7", "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Int, 5, vars.String, "trash", "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Int, 5, vars.Float, -7.9, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Int, 5, vars.Float, 4.9, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Int, timeTest.UnixMilli(), vars.Time, timeTest, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Int, 5, vars.Time, time.Now(), "res", vars.VariableValues{B: true}, shouldPass},

		{vars.Bool, true, vars.Int, 5, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Bool, false, vars.Int, 7, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, true, vars.Int, 0, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, true, vars.Bool, false, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, false, vars.Bool, false, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Bool, true, vars.String, "true", "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Bool, true, vars.String, "false", "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, true, vars.String, "trash", "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Bool, true, vars.Float, -7.9, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Bool, true, vars.Float, 0, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Bool, true, vars.Time, timeTest, "res", vars.VariableValues{B: false}, shouldFail},

		{vars.String, "5", vars.Int, 5, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, "5", vars.Int, 7, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "true", vars.Bool, false, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "true", vars.Bool, true, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, "trash", vars.Bool, false, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "5", vars.String, "5", "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, "5", vars.String, "7", "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "5", vars.Float, -7.9, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.String, "5", vars.Float, 4.9, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, strTime, vars.Time, timeTest, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.String, strTime, vars.Time, time.Now(), "res", vars.VariableValues{B: true}, shouldPass},

		{vars.Float, 5, vars.Int, 5, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Float, 5, vars.Int, 7, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Float, 5, vars.Bool, false, "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Float, 5, vars.String, "5", "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Float, 5, vars.String, "7", "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Float, 5, vars.String, "trash", "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Float, 5, vars.Float, -7.9, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Float, 4.9, vars.Float, 4.9, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Float, float64(timeTest.UnixMilli()), vars.Time, timeTest, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Float, 5, vars.Time, time.Now(), "res", vars.VariableValues{B: true}, shouldPass},

		{vars.Time, time.Time{}, vars.Int, 0, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Time, time.Now(), vars.Int, 7, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Time, time.Now(), vars.Bool, false, "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Time, timeTest, vars.String, strTime, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Time, time.Now(), vars.String, strTime, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Time, time.Now(), vars.String, "trash", "res", vars.VariableValues{I: -1}, shouldFail},
		{vars.Time, time.Now(), vars.Float, -7.9, "res", vars.VariableValues{B: true}, shouldPass},
		{vars.Time, timeTest, vars.Float, float64(timeTest.UnixMilli()), "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Time, timeTest, vars.Time, timeTest, "res", vars.VariableValues{B: false}, shouldPass},
		{vars.Time, timeTest, vars.Time, time.Now(), "res", vars.VariableValues{B: true}, shouldPass},
	}

	tests = map[string]struct {
		condFunc  func(y *vars.Variable, resName string) (gep.OpFunc, error)
		testCases []testCase
		opStr     string
	}{
		"equal":    {condFunc: Equal, testCases: equalTests, opStr: "=="},
		"notEqual": {condFunc: NotEqual, testCases: notEqualTests, opStr: "!="},
	}
)
