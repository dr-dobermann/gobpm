package adapters

import (
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// valueIface is data.Value as a reflect type, for the passthrough check.
var valueIface = reflect.TypeFor[data.Value]()

// buildAdapter is the reflection builder: it walks a struct type ONCE —
// exported fields, gobpm tags, kind classification — and returns the cached
// field table (ADR-011 v.6 §2.9.5). It is the only type walk in the package;
// everything after it is cached-index access.
func buildAdapter(t reflect.Type) (*typeAdapter, error) {
	if t.Kind() != reflect.Struct {
		return nil, errs.New(
			errs.M("adapters: %s is not a struct type", t.String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	ta := &typeAdapter{
		goType: t,
		name:   t.String(),
		byName: map[string]int{},
	}

	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported — always excluded
			continue
		}

		name, ok := fieldName(f)
		if !ok { // gobpm:"-"
			continue
		}

		if err := data.CheckName(name, errorClass); err != nil {
			return nil, err
		}

		if _, dup := ta.byName[name]; dup {
			return nil, errs.New(
				errs.M("adapters: %s maps two fields to the name %q",
					t.String(), name),
				errs.C(errorClass, errs.DuplicateObject))
		}

		fi := classify(f)
		fi.name = name
		fi.index = i

		ta.byName[name] = len(ta.fields)
		ta.fields = append(ta.fields, fi)
	}

	return ta, nil
}

// fieldName resolves a field's process name from its gobpm tag: "-" excludes,
// empty means the Go name, anything else renames (the part before a comma —
// the standard tag convention; options after it are a recognized future).
func fieldName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("gobpm")
	if tag == "-" {
		return "", false
	}

	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = f.Name
	}

	return name, true
}

// classify resolves a field's kind per the §4.10 resolution order.
func classify(f reflect.StructField) fieldInfo {
	return classifyType(f.Type)
}

// classifyType resolves how a Go type participates, per the §4.10 resolution
// order: a Register-ed custom adapter, a data.Value implementor
// (passthrough), a struct / pointer-to-struct (sub-record), a slice
// (collection view), or the opaque scalar leaf. Shared by the field builder
// and the slice-element views.
func classifyType(t reflect.Type) fieldInfo {
	if _, ok := customFor(t); ok {
		return fieldInfo{kind: kindCustom, goType: t}
	}

	if t.Implements(valueIface) {
		return fieldInfo{kind: kindPassthrough, goType: t}
	}

	if t.Kind() == reflect.Struct {
		return fieldInfo{kind: kindRecord, goType: t}
	}

	if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
		return fieldInfo{kind: kindRecord, goType: t.Elem(), isPtr: true}
	}

	if t.Kind() == reflect.Slice {
		return fieldInfo{kind: kindCollection, goType: t}
	}

	return fieldInfo{kind: kindLeaf, goType: t}
}
