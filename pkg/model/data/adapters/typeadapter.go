package adapters

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// fieldKind classifies how a struct field participates in the wrapped view
// (SRD-045 §4.10 resolution order).
type fieldKind string

const (
	// kindCustom — a Register-ed adapter factory answers for the field's type.
	kindCustom fieldKind = "custom"
	// kindPassthrough — the field's type implements data.Value; the held
	// value participates as itself (tier mixing, §4.7).
	kindPassthrough fieldKind = "passthrough"
	// kindRecord — a struct (or pointer-to-struct) field: a sub-record view.
	kindRecord fieldKind = "record"
	// kindCollection — a slice field: a live collection view.
	kindCollection fieldKind = "collection"
	// kindLeaf — anything else: a writable opaque scalar leaf (§4.5).
	kindLeaf fieldKind = "leaf"
)

// fieldInfo is one navigable field of the adapted type: its process name
// (tag-renamed), the cached struct index, its resolved kind, and — for
// record fields — the field's struct type (pointer already stripped).
type fieldInfo struct {
	goType reflect.Type
	name   string
	kind   fieldKind
	index  int
	isPtr  bool // kindRecord via a *Struct field
}

// typeAdapter is the cached per-type build product: the ordered field table
// (for structs) or the custom factory (for Register-ed types). Exactly one of
// fields/custom is meaningful.
type typeAdapter struct {
	byName map[string]int
	custom func(ptr reflect.Value) data.Value
	goType reflect.Type
	name   string
	fields []fieldInfo
}
