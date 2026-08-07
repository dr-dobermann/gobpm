// Package checkpoint owns the instance checkpoint document (ADR-033,
// SRD-070): the canonical value codec in this file turns committed
// process data into a tagged, schema-stable JSON form and back. The
// codec is private to the checkpoint layer on purpose — the public
// value API carries no wire format (SRD-070 §4.4).
package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"strconv"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

const errorClass = "CHECKPOINT"

// node is one encoded value: a tagged scalar (Scalar holds its string
// form) or a composite (exactly one of Items/Fields/Entries per Kind).
// Scalars ride as strings deliberately — strconv round-trips the whole
// integer family and float64 exactly, where JSON numbers would not.
type node struct {
	Entries map[string]node `json:"entries,omitempty"`
	Kind    string          `json:"kind"`
	Scalar  string          `json:"value,omitempty"`
	Items   []node          `json:"items,omitempty"`
	Fields  []field         `json:"fields,omitempty"`
}

// field is one record field (insertion-ordered, hence a slice).
type field struct {
	Name  string `json:"name"`
	Value node   `json:"value"`
}

// datum is one committed data.Data: its name, data-state and value.
type datum struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Value node   `json:"value"`
}

// The composite kind tags; scalar tags are the Go kind names.
const (
	kindArray  = "array"
	kindRecord = "record"
	kindMap    = "map"
	kindTime   = "time"
)

// EncodeData encodes one scope's committed data set. The scopePath
// rides only into error context — an unencodable payload must name
// where it lives (SRD-070 FR-2).
func EncodeData(
	ctx context.Context,
	scopePath string,
	dd []data.Data,
) (json.RawMessage, error) {
	out := make([]datum, 0, len(dd))

	for _, d := range dd {
		if d == nil {
			return nil, encErr(scopePath, "", "a nil datum isn't allowed")
		}

		n, err := encodeValue(ctx, scopePath, d.Name(), d.Value())
		if err != nil {
			return nil, err
		}

		out = append(out, datum{
			Name:  d.Name(),
			State: d.State().Name(),
			Value: n,
		})
	}

	// node is strings, maps of node, slices of node and field structs
	// throughout, and encodeValue builds a tree — so there is no type
	// json.Marshal rejects and no cycle it can hit. A failure here is a broken
	// invariant rather than an encoding error, and reports as one.
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, errs.Invariant("scope %q: encoding failed: %w", scopePath, err)
	}

	return raw, nil
}

// EncodeValue encodes ONE canonical value — the staged iteration
// outputs of an own-iteration composite (SRD-082 FR-1). where names
// the owner in error context, the EncodeData discipline.
func EncodeValue(
	ctx context.Context, where string, v data.Value,
) (json.RawMessage, error) {
	n, err := encodeValue(ctx, where, "staging", v)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(n)
	if err != nil {
		return nil, errs.Invariant("%s: staging encoding failed: %w", where, err)
	}

	return raw, nil
}

// DecodeValue rebuilds a value encoded by EncodeValue.
func DecodeValue(
	ctx context.Context, raw json.RawMessage,
) (data.Value, error) {
	var n node
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, errs.New(
			errs.M("staging decoding failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return decodeNode(ctx, n)
}

// DecodeData rebuilds the data set encoded by EncodeData.
func DecodeData(
	ctx context.Context,
	raw json.RawMessage,
) ([]data.Data, error) {
	var in []datum
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, errs.New(
			errs.M("checkpoint decoding failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	out := make([]data.Data, 0, len(in))

	for _, d := range in {
		v, err := decodeNode(ctx, d.Value)
		if err != nil {
			return nil, err
		}

		p, err := wrapDatum(d.Name, d.State, v)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

// encErr builds the classified codec error naming where the payload
// lives — never a silent skip (ADR-033 §2.1.3).
func encErr(scopePath, name, msg string) error {
	return errs.New(
		errs.M("checkpoint codec: "+msg),
		errs.C(errorClass, errs.InvalidParameter),
		errs.D(observability.AttrScopePath, scopePath),
		errs.D("datum", name))
}

// encodeValue encodes a data.Value: composites by their structural
// capability (a Value implements at most one), scalars by Get.
func encodeValue(
	ctx context.Context, scopePath, name string, v data.Value,
) (node, error) {
	if v == nil {
		return node{}, encErr(scopePath, name, "a nil value isn't allowed")
	}

	switch t := v.(type) {
	case data.Record:
		return encodeRecord(ctx, scopePath, name, t)

	case data.Map:
		return encodeMap(ctx, scopePath, name, t)

	case data.Collection:
		return encodeArray(ctx, scopePath, name, t)

	default:
		return encodeAny(ctx, scopePath, name, v.Get(ctx))
	}
}

// encodeRecord walks the fields in insertion order.
func encodeRecord(
	ctx context.Context, scopePath, name string, r data.Record,
) (node, error) {
	n := node{Kind: kindRecord, Fields: []field{}}

	for _, key := range r.Keys() {
		fv, err := r.Field(ctx, key)
		if err != nil {
			return node{}, encErr(scopePath, name,
				"record field "+key+" isn't readable: "+err.Error())
		}

		fn, err := encodeValue(ctx, scopePath, name+"."+key, fv)
		if err != nil {
			return node{}, err
		}

		n.Fields = append(n.Fields, field{Name: key, Value: fn})
	}

	return n, nil
}

// encodeMap walks the entries in sorted-key order.
func encodeMap(
	ctx context.Context, scopePath, name string, m data.Map,
) (node, error) {
	n := node{Kind: kindMap, Entries: map[string]node{}}

	for _, key := range m.Keys() {
		ev, err := m.Entry(ctx, key)
		if err != nil {
			return node{}, encErr(scopePath, name,
				"map entry "+key+" isn't readable: "+err.Error())
		}

		en, err := encodeAny(ctx, scopePath, name+"["+key+"]", ev)
		if err != nil {
			return node{}, err
		}

		n.Entries[key] = en
	}

	return n, nil
}

// encodeArray encodes the elements positionally.
func encodeArray(
	ctx context.Context, scopePath, name string, c data.Collection,
) (node, error) {
	n := node{Kind: kindArray, Items: []node{}}

	for i, el := range c.GetAll(ctx) {
		en, err := encodeAny(ctx, scopePath,
			name+"["+strconv.Itoa(i)+"]", el)
		if err != nil {
			return node{}, err
		}

		n.Items = append(n.Items, en)
	}

	return n, nil
}

// encodeAny encodes a raw element: a nested data.Value recurses, a
// scalar takes its kind tag, anything else is loud.
func encodeAny(
	ctx context.Context, scopePath, name string, x any,
) (node, error) {
	if v, ok := x.(data.Value); ok {
		return encodeValue(ctx, scopePath, name, v)
	}

	if n, ok := encodeIntScalar(x); ok {
		return n, nil
	}

	switch t := x.(type) {
	case bool:
		return node{Kind: "bool", Scalar: strconv.FormatBool(t)}, nil
	case string:
		return node{Kind: "string", Scalar: t}, nil
	case float32:
		return node{Kind: "float32",
			Scalar: strconv.FormatFloat(float64(t), 'g', -1, 32)}, nil
	case float64:
		return node{Kind: "float64",
			Scalar: strconv.FormatFloat(t, 'g', -1, 64)}, nil
	case time.Time:
		return node{Kind: kindTime,
			Scalar: t.Format(time.RFC3339Nano)}, nil

	default:
		return node{}, encErr(scopePath, name,
			fmt.Sprintf("payload of type %T isn't checkpoint-codable "+
				"(no structural capability, not a canonical scalar)", x))
	}
}

// encodeIntScalar covers the integer family (split from encodeAny to
// keep both switches readable).
func encodeIntScalar(x any) (node, bool) {
	switch t := x.(type) {
	case int:
		return node{Kind: "int", Scalar: strconv.FormatInt(int64(t), 10)}, true
	case int8:
		return node{Kind: "int8", Scalar: strconv.FormatInt(int64(t), 10)}, true
	case int16:
		return node{Kind: "int16", Scalar: strconv.FormatInt(int64(t), 10)}, true
	case int32:
		return node{Kind: "int32", Scalar: strconv.FormatInt(int64(t), 10)}, true
	case int64:
		return node{Kind: "int64", Scalar: strconv.FormatInt(t, 10)}, true
	case uint:
		return node{Kind: "uint", Scalar: strconv.FormatUint(uint64(t), 10)}, true
	case uint8:
		return node{Kind: "uint8", Scalar: strconv.FormatUint(uint64(t), 10)}, true
	case uint16:
		return node{Kind: "uint16", Scalar: strconv.FormatUint(uint64(t), 10)}, true
	case uint32:
		return node{Kind: "uint32", Scalar: strconv.FormatUint(uint64(t), 10)}, true
	case uint64:
		return node{Kind: "uint64", Scalar: strconv.FormatUint(t, 10)}, true
	}

	return node{}, false
}
