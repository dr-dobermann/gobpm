package checkpoint_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

var ctx = context.Background()

// mkDatum wraps a value as a Ready parameter.
func mkDatum(t *testing.T, name string, v data.Value) data.Data {
	t.Helper()

	p, err := data.ReadyValueParameter(name, v)
	require.NoError(t, err)

	return p
}

// roundTrip encodes one datum and decodes it back.
func roundTrip(t *testing.T, name string, v data.Value) data.Data {
	t.Helper()

	raw, err := checkpoint.EncodeData(ctx, "/proc", []data.Data{
		mkDatum(t, name, v),
	})
	require.NoError(t, err)

	dd, err := checkpoint.DecodeData(ctx, raw)
	require.NoError(t, err)
	require.Len(t, dd, 1)
	require.Equal(t, name, dd[0].Name())
	require.Equal(t, data.StateReady, dd[0].State().Name())

	return dd[0]
}

// TestScalarRoundTrip covers SRD-070 T-1's scalar half: every canonical
// kind returns with its exact Go type and value.
func TestScalarRoundTrip(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	when := time.Date(2026, 7, 26, 15, 4, 5, 123456789, time.UTC)

	cases := []struct {
		name string
		v    data.Value
		want any
	}{
		{"bool", values.NewVariable(true), true},
		{"string", values.NewVariable("привет"), "привет"},
		{"int", values.NewVariable(int(-42)), int(-42)},
		{"int8", values.NewVariable(int8(-8)), int8(-8)},
		{"int16", values.NewVariable(int16(-16)), int16(-16)},
		{"int32", values.NewVariable(int32(-32)), int32(-32)},
		{"int64", values.NewVariable(int64(-64)), int64(-64)},
		{"uint", values.NewVariable(uint(42)), uint(42)},
		{"uint8", values.NewVariable(uint8(8)), uint8(8)},
		{"uint16", values.NewVariable(uint16(16)), uint16(16)},
		{"uint32", values.NewVariable(uint32(32)), uint32(32)},
		{"uint64", values.NewVariable(uint64(18446744073709551615)),
			uint64(18446744073709551615)},
		{"float32", values.NewVariable(float32(1.5)), float32(1.5)},
		{"float64", values.NewVariable(0.1), 0.1},
		{"time", values.NewVariable(when), when},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := roundTrip(t, c.name, c.v).Value().Get(ctx)

			if w, ok := c.want.(time.Time); ok {
				require.True(t, w.Equal(got.(time.Time)),
					"time must round-trip to the nanosecond")

				return
			}

			require.Equal(t, c.want, got,
				"the exact Go type and value must survive")
		})
	}
}

// TestCompositeRoundTrip covers the composite half: arrays, records,
// maps and their nesting keep shape, order and element typing.
func TestCompositeRoundTrip(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("typed scalar array",
		func(t *testing.T) {
			got := roundTrip(t, "skus",
				values.NewArray("sku-1", "sku-2", "sku-3"))

			arr, ok := got.Value().(data.Collection)
			require.True(t, ok)
			require.Equal(t, 3, arr.Count())
			require.Equal(t,
				[]any{"sku-1", "sku-2", "sku-3"}, arr.GetAll(ctx))
		})

	t.Run("record keeps field order and nesting",
		func(t *testing.T) {
			customer, err := values.NewRecord(
				values.F("tier", values.NewVariable("vip")))
			require.NoError(t, err)

			order, err := values.NewRecord(
				values.F("total", values.NewVariable(150)),
				values.F("customer", customer))
			require.NoError(t, err)

			got := roundTrip(t, "order", order)

			rec, ok := got.Value().(data.Record)
			require.True(t, ok)
			require.Equal(t, []string{"total", "customer"}, rec.Keys())

			total, err := rec.Field(ctx, "total")
			require.NoError(t, err)
			require.Equal(t, 150, total.Get(ctx))

			cust, err := rec.Field(ctx, "customer")
			require.NoError(t, err)

			nested, ok := cust.(data.Record)
			require.True(t, ok)

			tier, err := nested.Field(ctx, "tier")
			require.NoError(t, err)
			require.Equal(t, "vip", tier.Get(ctx))
		})

	t.Run("typed map",
		func(t *testing.T) {
			rates, err := values.NewMap(map[string]float64{
				"EUR": 1.09,
				"USD": 0.92,
			})
			require.NoError(t, err)

			got := roundTrip(t, "rates", rates)

			m, ok := got.Value().(data.Map)
			require.True(t, ok)
			require.Equal(t, []string{"EUR", "USD"}, m.Keys())

			eur, err := m.Entry(ctx, "EUR")
			require.NoError(t, err)
			require.Equal(t, 1.09, eur)
		})

	t.Run("array of records (heterogeneous elements)",
		func(t *testing.T) {
			r1, err := values.NewRecord(
				values.F("lane", values.NewVariable("fast")))
			require.NoError(t, err)

			r2, err := values.NewRecord(
				values.F("lane", values.NewVariable("slow")))
			require.NoError(t, err)

			got := roundTrip(t, "rows",
				values.NewArray[data.Value](r1, r2))

			arr, ok := got.Value().(data.Collection)
			require.True(t, ok)
			require.Equal(t, 2, arr.Count())

			first, err := arr.GetAt(ctx, 0)
			require.NoError(t, err)

			rec, ok := first.(data.Record)
			require.True(t, ok)

			lane, err := rec.Field(ctx, "lane")
			require.NoError(t, err)
			require.Equal(t, "fast", lane.Get(ctx))
		})

	t.Run("record inside a map",
		func(t *testing.T) {
			inner, err := values.NewRecord(
				values.F("n", values.NewVariable(int64(7))))
			require.NoError(t, err)

			m, err := values.NewMap(map[string]data.Value{"a": inner})
			require.NoError(t, err)

			got := roundTrip(t, "mix", m)

			mm, ok := got.Value().(data.Map)
			require.True(t, ok)

			e, err := mm.Entry(ctx, "a")
			require.NoError(t, err)

			rec, ok := e.(data.Record)
			require.True(t, ok)

			n, err := rec.Field(ctx, "n")
			require.NoError(t, err)
			require.Equal(t, int64(7), n.Get(ctx))
		})
}

// TestCodecErrors covers the loud half: uncodable payloads name their
// scope path and datum; garbage input fails classified.
func TestCodecErrors(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("an uncodable payload names path and datum",
		func(t *testing.T) {
			_, err := checkpoint.EncodeData(ctx, "/proc/sp-1", []data.Data{
				mkDatum(t, "hot", values.NewVariable(make(chan int))),
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "/proc/sp-1")
			require.Contains(t, err.Error(), "hot")
			require.Contains(t, err.Error(), "isn't checkpoint-codable")
		})

	t.Run("a nil datum is rejected",
		func(t *testing.T) {
			_, err := checkpoint.EncodeData(ctx, "/proc", []data.Data{nil})
			require.Error(t, err)
		})

	t.Run("garbage bytes fail loud",
		func(t *testing.T) {
			_, err := checkpoint.DecodeData(ctx, []byte("not json"))
			require.Error(t, err)
			require.Contains(t, err.Error(), "decoding failed")
		})

	t.Run("an unknown kind fails loud",
		func(t *testing.T) {
			_, err := checkpoint.DecodeData(ctx,
				[]byte(`[{"name":"x","state":"Ready",`+
					`"value":{"kind":"quantum"}}]`))
			require.Error(t, err)
			require.Contains(t, err.Error(), "unknown value kind")
		})

	t.Run("a broken scalar fails loud",
		func(t *testing.T) {
			_, err := checkpoint.DecodeData(ctx,
				[]byte(`[{"name":"x","state":"Ready",`+
					`"value":{"kind":"int","value":"NaN"}}]`))
			require.Error(t, err)
			require.Contains(t, err.Error(), "doesn't parse")
		})
}

// TestStateRoundTrip: a non-default data-state survives by name.
func TestStateRoundTrip(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	item, err := data.NewItemDefinition(values.NewVariable(1))
	require.NoError(t, err)

	iae, err := data.NewItemAwareElement(item, data.UnavailableDataState)
	require.NoError(t, err)

	p, err := data.NewParameter("cold", iae)
	require.NoError(t, err)

	raw, err := checkpoint.EncodeData(ctx, "/proc", []data.Data{p})
	require.NoError(t, err)

	dd, err := checkpoint.DecodeData(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, data.StateUnavailable, dd[0].State().Name())
}

// TestDecodeEdgeCases lifts the defensive relays: a custom data-state
// rebuilds by name, an empty datum name fails the rebuild, and a broken
// entry inside a uniform map relays its parse error.
func TestDecodeEdgeCases(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("a custom state name rebuilds",
		func(t *testing.T) {
			dd, err := checkpoint.DecodeData(ctx,
				[]byte(`[{"name":"x","state":"ARCHIVED",`+
					`"value":{"kind":"int","value":"1"}}]`))
			require.NoError(t, err)
			require.Equal(t, "ARCHIVED", dd[0].State().Name())
		})

	t.Run("an empty state name fails the rebuild",
		func(t *testing.T) {
			_, err := checkpoint.DecodeData(ctx,
				[]byte(`[{"name":"x","state":"",`+
					`"value":{"kind":"int","value":"1"}}]`))
			require.Error(t, err)
			require.Contains(t, err.Error(), "state rebuild failed")
		})

	t.Run("an empty datum name fails the rebuild",
		func(t *testing.T) {
			_, err := checkpoint.DecodeData(ctx,
				[]byte(`[{"name":"","state":"Ready",`+
					`"value":{"kind":"int","value":"1"}}]`))
			require.Error(t, err)
		})

	t.Run("a broken entry in a uniform map relays",
		func(t *testing.T) {
			_, err := checkpoint.DecodeData(ctx,
				[]byte(`[{"name":"m","state":"Ready","value":{"kind":"map",`+
					`"entries":{"a":{"kind":"int","value":"1"},`+
					`"b":{"kind":"int","value":"zzz"}}}}]`))
			require.Error(t, err)
			require.Contains(t, err.Error(), "doesn't parse")
		})

	t.Run("a broken element in a uniform array relays",
		func(t *testing.T) {
			_, err := checkpoint.DecodeData(ctx,
				[]byte(`[{"name":"a","state":"Ready","value":{"kind":"array",`+
					`"items":[{"kind":"bool","value":"true"},`+
					`{"kind":"bool","value":"zzz"}]}}]`))
			require.Error(t, err)
		})
}
