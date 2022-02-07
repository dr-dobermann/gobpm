package operations

import (
	"fmt"
	"math"
	"testing"
	"time"

	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

type typeVal struct {
	tt vars.Type
	tv interface{}
}

type tCase struct {
	src     typeVal
	mustErr bool
	res     vars.Values
}

type testDesrc struct {
	dst   typeVal
	cases []tCase
}

func TestAddOperations(t *testing.T) {
	const (
		testShouldFail bool = true
		testShouldPass      = false
	)
	resName := "add_res"

	strTest := "dober "
	strTime := "1973-02-23T05:15:00+06:00"

	timeTest, err := time.Parse(time.RFC3339, strTime)
	if err != nil {
		t.Fatal("couldn't convert time:", err)
	}

	intCases := []tCase{
		{
			src:     typeVal{tt: vars.Int, tv: 5}, // int + int
			mustErr: testShouldPass,
			res:     vars.Values{I: 10}},
		{
			src:     typeVal{tt: vars.Bool, tv: false}, // int + bool
			mustErr: testShouldFail,
			res:     vars.Values{B: false}},
		{
			src:     typeVal{tt: vars.String, tv: "10"}, // int + "10"
			mustErr: testShouldPass,
			res:     vars.Values{I: 15}},
		{
			src:     typeVal{tt: vars.String, tv: "trash"}, // int + "trash"
			mustErr: testShouldFail,
			res:     vars.Values{B: false}},
		{
			src:     typeVal{tt: vars.Float, tv: 6.7}, // int + float64
			mustErr: testShouldPass,
			res:     vars.Values{I: 12}},
		{
			src:     typeVal{tt: vars.Time, tv: timeTest}, // int + time.Time
			mustErr: testShouldPass,
			res:     vars.Values{I: timeTest.UnixMilli() + 5}},
	}

	boolCases := []tCase{
		{
			src:     typeVal{tt: vars.Int, tv: 10}, // bool + int
			mustErr: testShouldFail,
			res:     vars.Values{B: true}},
		{
			src:     typeVal{tt: vars.Bool, tv: false}, // bool + int
			mustErr: testShouldFail,
			res:     vars.Values{B: true}},
		{
			src:     typeVal{tt: vars.String, tv: "true"}, // bool + string
			mustErr: testShouldFail,
			res:     vars.Values{B: true}},
		{
			src:     typeVal{tt: vars.Float, tv: 5.7}, // bool + float
			mustErr: testShouldFail,
			res:     vars.Values{B: true}},
		{
			src:     typeVal{tt: vars.Time, tv: timeTest}, // bool + time
			mustErr: testShouldFail,
			res:     vars.Values{B: true}}}

	strCases := []tCase{
		{
			src:     typeVal{tt: vars.Int, tv: 5}, // string + int
			mustErr: testShouldPass,
			res:     vars.Values{S: strTest + "5"}},
		{
			src:     typeVal{tt: vars.Bool, tv: false}, // string + bool
			mustErr: testShouldPass,
			res:     vars.Values{S: strTest + "false"}},
		{
			src:     typeVal{tt: vars.String, tv: " cool"}, // string + string
			mustErr: testShouldPass,
			res:     vars.Values{S: strTest + " cool"}},
		{
			src:     typeVal{tt: vars.Float, tv: 7.3}, // string + float
			mustErr: testShouldPass,
			res:     vars.Values{S: strTest + "7.30"}},
		{
			src:     typeVal{tt: vars.Time, tv: timeTest}, // string + time
			mustErr: testShouldPass,
			res:     vars.Values{S: strTest + strTime}}}

	floatCases := []tCase{
		{
			src:     typeVal{tt: vars.Int, tv: 5}, // float + int
			mustErr: testShouldPass,
			res:     vars.Values{F: 16.7}},
		{
			src:     typeVal{tt: vars.Bool, tv: true}, // float + bool
			mustErr: testShouldFail,
			res:     vars.Values{B: false}},
		{
			src:     typeVal{tt: vars.String, tv: "7"}, // float + string
			mustErr: testShouldPass,
			res:     vars.Values{F: 18.7}},
		{
			src:     typeVal{tt: vars.String, tv: "trash"}, // float + invalid string
			mustErr: testShouldFail,
			res:     vars.Values{B: false}},
		{
			src:     typeVal{tt: vars.Time, tv: timeTest}, // float + time
			mustErr: testShouldFail,
			res:     vars.Values{F: 16.7}},
	}

	timeCases := []tCase{
		{
			src:     typeVal{tt: vars.Int, tv: int64(5 * time.Minute)}, // time + int
			mustErr: testShouldPass,
			res:     vars.Values{T: timeTest.Add(5 * time.Minute)}},
		{
			src:     typeVal{tt: vars.Bool, tv: false}, // time + bool
			mustErr: testShouldFail,
			res:     vars.Values{B: false}},
		{
			src:     typeVal{tt: vars.String, tv: "test"}, // time + string
			mustErr: testShouldFail,
			res:     vars.Values{B: false}},
		{
			src:     typeVal{tt: vars.Float, tv: 13.0 * float64(time.Minute)}, // time + float
			mustErr: testShouldPass,
			res:     vars.Values{T: timeTest.Add(13 * time.Minute)}},
		{
			src:     typeVal{tt: vars.Time, tv: time.Now()}, // time + time
			mustErr: testShouldFail,
			res:     vars.Values{B: false}}}

	testCases := []testDesrc{
		{dst: typeVal{vars.Int, 5}, cases: intCases},
		{dst: typeVal{vars.Bool, true}, cases: boolCases},
		{dst: typeVal{vars.String, strTest}, cases: strCases},
		{dst: typeVal{vars.Float, 11.7}, cases: floatCases},
		{dst: typeVal{vars.Time, timeTest}, cases: timeCases},
	}

	for _, tc := range testCases {
		tc := tc

		for _, c := range tc.cases {
			c := c

			testName := fmt.Sprintf(
				"%v(%v) + %v(%v)",
				tc.dst.tt, tc.dst.tv, c.src.tt, c.src.tv)
			t.Run(
				testName,
				func(t *testing.T) {
					opF, err := Add(
						vars.V(c.src.tt.String(), c.src.tt, c.src.tv),
						resName)
					if err != nil {
						t.Fatal("couldn't get add OpFunc", err)
					}

					res, err := opF(
						vars.V(tc.dst.tt.String(), tc.dst.tt, tc.dst.tv))
					if err != nil {
						if !c.mustErr {
							t.Fatalf("operation should not "+
								"return an error: %v", err)
						}

						return
					}

					if c.mustErr {
						t.Fatalf(
							"operation should return an error: %s",
							testName)
					}

					if !checkRes(tc.dst.tt, c.res, *res) {
						t.Fatalf("invalid results: want %v, got %v",
							c.res, res.Values)
					}
				})
		}
	}
}

func checkRes(
	t vars.Type,
	vv vars.Values,
	res vars.Variable) bool {

	switch t {
	case vars.Int:
		return res.I == vv.I

	case vars.Bool:
		return res.B == vv.B

	case vars.String:
		return res.S == vv.S

	case vars.Float:
		precMult := math.Pow10(res.Precision())

		return math.Round(res.F*precMult) == math.Round(vv.F*precMult)

	case vars.Time:
		return res.T.Equal(vv.T)
	}

	return true
}
