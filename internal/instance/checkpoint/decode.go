package checkpoint

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// decErr builds the classified decode error.
func decErr(kind, msg string) error {
	return errs.New(
		errs.M("checkpoint codec: "+msg),
		errs.C(errorClass, errs.InvalidParameter),
		errs.D("kind", kind))
}

// scalarDecoders is the kind→builder table (data-over-code): each
// parses the scalar's string form back into a typed values.Variable.
var scalarDecoders = map[string]func(s string) (data.Value, error){
	"bool": func(s string) (data.Value, error) {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return nil, err
		}

		return values.NewVariable(v), nil
	},
	"string": func(s string) (data.Value, error) {
		return values.NewVariable(s), nil
	},
	"int":    intDecoder(func(v int64) data.Value { return values.NewVariable(int(v)) }),
	"int8":   intDecoder(func(v int64) data.Value { return values.NewVariable(int8(v)) }),
	"int16":  intDecoder(func(v int64) data.Value { return values.NewVariable(int16(v)) }),
	"int32":  intDecoder(func(v int64) data.Value { return values.NewVariable(int32(v)) }),
	"int64":  intDecoder(func(v int64) data.Value { return values.NewVariable(v) }),
	"uint":   uintDecoder(func(v uint64) data.Value { return values.NewVariable(uint(v)) }),
	"uint8":  uintDecoder(func(v uint64) data.Value { return values.NewVariable(uint8(v)) }),
	"uint16": uintDecoder(func(v uint64) data.Value { return values.NewVariable(uint16(v)) }),
	"uint32": uintDecoder(func(v uint64) data.Value { return values.NewVariable(uint32(v)) }),
	"uint64": uintDecoder(func(v uint64) data.Value { return values.NewVariable(v) }),
	"float32": func(s string) (data.Value, error) {
		v, err := strconv.ParseFloat(s, 32)
		if err != nil {
			return nil, err
		}

		return values.NewVariable(float32(v)), nil
	},
	"float64": func(s string) (data.Value, error) {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, err
		}

		return values.NewVariable(v), nil
	},
	kindTime: func(s string) (data.Value, error) {
		v, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, err
		}

		return values.NewVariable(v), nil
	},
}

// intDecoder/uintDecoder fold the integer-family parsing boilerplate.
func intDecoder(wrap func(int64) data.Value) func(string) (data.Value, error) {
	return func(s string) (data.Value, error) {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, err
		}

		return wrap(v), nil
	}
}

func uintDecoder(wrap func(uint64) data.Value) func(string) (data.Value, error) {
	return func(s string) (data.Value, error) {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, err
		}

		return wrap(v), nil
	}
}

// decodeNode rebuilds a data.Value from its encoded node.
func decodeNode(ctx context.Context, n node) (data.Value, error) {
	switch n.Kind {
	case kindRecord:
		return decodeRecord(ctx, n)

	case kindMap:
		return decodeMap(ctx, n)

	case kindArray:
		return decodeArray(ctx, n)

	default:
		dec, ok := scalarDecoders[n.Kind]
		if !ok {
			return nil, decErr(n.Kind, "unknown value kind")
		}

		v, err := dec(n.Scalar)
		if err != nil {
			return nil, decErr(n.Kind,
				"scalar "+strconv.Quote(n.Scalar)+" doesn't parse: "+
					err.Error())
		}

		return v, nil
	}
}

// decodeRecord rebuilds a values.Record preserving field order.
func decodeRecord(ctx context.Context, n node) (data.Value, error) {
	ff := make([]values.RecordField, 0, len(n.Fields))

	for _, f := range n.Fields {
		fv, err := decodeNode(ctx, f.Value)
		if err != nil {
			return nil, err
		}

		ff = append(ff, values.F(f.Name, fv))
	}

	r, err := values.NewRecord(ff...)
	if err != nil {
		return nil, decErr(kindRecord, "record rebuild failed: "+err.Error())
	}

	return r, nil
}

// decodeMap rebuilds a values.Map. A homogeneous scalar entry set
// rebuilds under its raw Go element type (the shape Map[T] committed);
// mixed or composite entries rebuild as Map[data.Value].
func decodeMap(ctx context.Context, n node) (data.Value, error) {
	keys := make([]string, 0, len(n.Entries))
	for k := range n.Entries {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	if kind, uniform := uniformScalarKind(mapNodes(n, keys)); uniform {
		return mapBuilders[kind](ctx, n, keys)
	}

	entries := map[string]data.Value{}

	for _, k := range keys {
		v, err := decodeNode(ctx, n.Entries[k])
		if err != nil {
			return nil, err
		}

		entries[k] = v
	}

	m, err := values.NewMap(entries)
	if err != nil {
		return nil, decErr(kindMap, "map rebuild failed: "+err.Error())
	}

	return m, nil
}

// decodeArray rebuilds a values.Array: uniform scalar elements under
// their raw Go element type, anything else as Array[data.Value].
func decodeArray(ctx context.Context, n node) (data.Value, error) {
	if kind, uniform := uniformScalarKind(n.Items); uniform {
		return arrayBuilders[kind](ctx, n.Items)
	}

	elems := make([]data.Value, 0, len(n.Items))

	for _, item := range n.Items {
		v, err := decodeNode(ctx, item)
		if err != nil {
			return nil, err
		}

		elems = append(elems, v)
	}

	return values.NewArray(elems...), nil
}

// typedArray/typedMap rebuild a composite under its concrete element
// type, keeping the committed shape's typing across the round trip.
func typedArray[T any](ctx context.Context, items []node) (data.Value, error) {
	elems := make([]T, 0, len(items))

	for _, it := range items {
		v, err := decodeNode(ctx, it)
		if err != nil {
			return nil, err
		}

		e, ok := v.Get(ctx).(T)
		if !ok {
			return nil, decErr(it.Kind, "array element type mismatch")
		}

		elems = append(elems, e)
	}

	return values.NewArray(elems...), nil
}

func typedMap[T any](
	ctx context.Context, n node, keys []string,
) (data.Value, error) {
	entries := make(map[string]T, len(keys))

	for _, k := range keys {
		v, err := decodeNode(ctx, n.Entries[k])
		if err != nil {
			return nil, err
		}

		e, ok := v.Get(ctx).(T)
		if !ok {
			return nil, decErr(n.Entries[k].Kind, "map entry type mismatch")
		}

		entries[k] = e
	}

	m, err := values.NewMap(entries)
	if err != nil {
		return nil, decErr(kindMap, "map rebuild failed: "+err.Error())
	}

	return m, nil
}

// arrayBuilders/mapBuilders dispatch a uniform scalar kind onto its
// typed rebuilder (data-over-code). Populated in init — the builders
// recurse through decodeNode, so literal initialization would cycle.
var (
	arrayBuilders map[string]func(context.Context, []node) (data.Value, error)
	mapBuilders   map[string]func(context.Context, node, []string) (data.Value, error)
)

func init() { //nolint:gochecknoinits // breaks the decodeNode init cycle
	arrayBuilders = map[string]func(context.Context, []node) (data.Value, error){
		"bool":    typedArray[bool],
		"string":  typedArray[string],
		"int":     typedArray[int],
		"int8":    typedArray[int8],
		"int16":   typedArray[int16],
		"int32":   typedArray[int32],
		"int64":   typedArray[int64],
		"uint":    typedArray[uint],
		"uint8":   typedArray[uint8],
		"uint16":  typedArray[uint16],
		"uint32":  typedArray[uint32],
		"uint64":  typedArray[uint64],
		"float32": typedArray[float32],
		"float64": typedArray[float64],
		kindTime:  typedArray[time.Time],
	}

	mapBuilders = map[string]func(context.Context, node, []string) (data.Value, error){
		"bool":    typedMap[bool],
		"string":  typedMap[string],
		"int":     typedMap[int],
		"int8":    typedMap[int8],
		"int16":   typedMap[int16],
		"int32":   typedMap[int32],
		"int64":   typedMap[int64],
		"uint":    typedMap[uint],
		"uint8":   typedMap[uint8],
		"uint16":  typedMap[uint16],
		"uint32":  typedMap[uint32],
		"uint64":  typedMap[uint64],
		"float32": typedMap[float32],
		"float64": typedMap[float64],
		kindTime:  typedMap[time.Time],
	}
}

// uniformScalarKind reports whether every node is a scalar of one same
// kind (composites disqualify).
func uniformScalarKind(nn []node) (string, bool) {
	kind := ""

	for _, n := range nn {
		switch n.Kind {
		case kindArray, kindRecord, kindMap:
			return "", false
		}

		if kind == "" {
			kind = n.Kind

			continue
		}

		if n.Kind != kind {
			return "", false
		}
	}

	return kind, kind != ""
}

// mapNodes lists the entries' nodes in key order.
func mapNodes(n node, keys []string) []node {
	out := make([]node, 0, len(keys))
	for _, k := range keys {
		out = append(out, n.Entries[k])
	}

	return out
}

// defaultStates maps the engine's default data-state names onto their
// registered globals (data-over-code).
func defaultStates() map[string]*data.SrcState {
	return map[string]*data.SrcState{
		data.StateReady:       data.ReadyDataState,
		data.StateUnavailable: data.UnavailableDataState,
		data.StateUndefined:   data.UndefinedSrcState,
	}
}

// wrapDatum re-wraps a decoded value as a named parameter in its
// recorded data-state.
func wrapDatum(name, stateName string, v data.Value) (data.Data, error) {
	item, err := data.NewItemDefinition(v)
	if err != nil {
		return nil, decErr("datum", name+": item rebuild failed: "+err.Error())
	}

	state, ok := defaultStates()[stateName]
	if !ok {
		state, err = data.NewSrcState(stateName)
		if err != nil {
			return nil, decErr("datum",
				name+": state rebuild failed: "+err.Error())
		}
	}

	iae, err := data.NewItemAwareElement(item, state)
	if err != nil {
		return nil, decErr("datum", name+": rebuild failed: "+err.Error())
	}

	return data.NewParameter(name, iae)
}
