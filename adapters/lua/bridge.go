package lua

import (
	"fmt"

	glua "github.com/yuin/gopher-lua"
)

// toLua bridges a Go value (what data.Value.Get yields) into a Lua value.
// bool, integers, floats and strings map directly; string-keyed maps and
// slices recurse into tables. Anything else is a loud error — never a
// silent nil (ADR-031 §2.4).
func toLua(l *glua.LState, v any) (glua.LValue, error) {
	switch tv := v.(type) {
	case nil:
		return glua.LNil, nil

	case bool:
		return glua.LBool(tv), nil

	case int:
		return glua.LNumber(tv), nil

	case int64:
		return glua.LNumber(tv), nil

	case float64:
		return glua.LNumber(tv), nil

	case string:
		return glua.LString(tv), nil

	case map[string]any:
		t := l.NewTable()

		for k, mv := range tv {
			lv, err := toLua(l, mv)
			if err != nil {
				return nil, err
			}

			t.RawSetString(k, lv)
		}

		return t, nil

	case []any:
		t := l.NewTable()

		for _, sv := range tv {
			lv, err := toLua(l, sv)
			if err != nil {
				return nil, err
			}

			t.Append(lv)
		}

		return t, nil
	}

	return nil, fmt.Errorf("lua: unmappable Go value of type %T", v)
}

// toGo bridges a Lua value into a Go value for the script's outputs.
// Numbers land as float64 — Lua's single number type (documented on the
// engine). A sequence table becomes []any, any other table map[string]any
// with string keys required; functions, userdata and channels are loud
// errors.
func toGo(v glua.LValue) (any, error) {
	switch tv := v.(type) {
	case *glua.LNilType:
		return nil, nil

	case glua.LBool:
		return bool(tv), nil

	case glua.LNumber:
		return float64(tv), nil

	case glua.LString:
		return string(tv), nil

	case *glua.LTable:
		return tableToGo(tv)
	}

	return nil, fmt.Errorf("lua: unmappable Lua value of type %s",
		v.Type().String())
}

// tableToGo converts a table: a pure sequence (1..N) becomes []any, else a
// string-keyed map.
func tableToGo(t *glua.LTable) (any, error) {
	seqLen := t.Len()

	// a pure sequence: exactly seqLen entries, keys 1..N.
	entries := 0
	t.ForEach(func(glua.LValue, glua.LValue) { entries++ })

	if seqLen > 0 && entries == seqLen {
		out := make([]any, 0, seqLen)

		for i := 1; i <= seqLen; i++ {
			gv, err := toGo(t.RawGetInt(i))
			if err != nil {
				return nil, err
			}

			out = append(out, gv)
		}

		return out, nil
	}

	out := make(map[string]any, entries)

	var convErr error

	t.ForEach(func(k, v glua.LValue) {
		if convErr != nil {
			return
		}

		ks, ok := k.(glua.LString)
		if !ok {
			convErr = fmt.Errorf(
				"lua: a table key of type %s isn't mappable "+
					"(string keys required)", k.Type().String())

			return
		}

		gv, err := toGo(v)
		if err != nil {
			convErr = err

			return
		}

		out[string(ks)] = gv
	})

	if convErr != nil {
		return nil, convErr
	}

	return out, nil
}
