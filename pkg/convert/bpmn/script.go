package bpmn

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// luaScriptFormats are the scriptFormat hints the Lua battery claims.
//
// They are written out rather than read from the battery because
// adapters/lua is a SEPARATE MODULE that the root module does not require
// — importing it here is not possible, and adding the dependency to read
// three strings would invert the layering (a converter in core depending
// on an adapter). TestLuaFormatsMatchTheBattery reads the adapter's source
// from disk and fails if these drift apart, which is the same technique
// internal/lintcfg uses to hold the observability vocabulary together
// across files it cannot import.
var luaScriptFormats = []string{"text/x-lua", "application/x-lua", "lua"}

// gofuncScriptFormats are the hints of the dependency-free battery, listed
// so its refusal can give the real reason rather than "unknown format".
var gofuncScriptFormats = []string{"application/x-gobpm-gofunc", "gofunc"}

// scriptFormatVerdict is what the converter does with a script task's
// declared format.
type scriptFormatVerdict uint8

const (
	// scriptRefused is the zero value: a format nothing claims.
	scriptRefused scriptFormatVerdict = iota

	// scriptLua imports — the body is self-contained source the engine
	// interprets.
	scriptLua

	// scriptByReference is refused with its own reason: the format is
	// real and the engine ships an implementation, but the script text
	// names host code rather than carrying behavior.
	scriptByReference
)

// classifyScriptFormat decides what a declared scriptFormat means.
func classifyScriptFormat(format string) scriptFormatVerdict {
	f := strings.ToLower(strings.TrimSpace(format))

	for _, claimed := range luaScriptFormats {
		if f == claimed {
			return scriptLua
		}
	}

	for _, claimed := range gofuncScriptFormats {
		if f == claimed {
			return scriptByReference
		}
	}

	return scriptRefused
}

// refuseScriptFormat reports a script task the converter will not import,
// with the reason that fits its format — the three are genuinely
// different problems and a single "unsupported" message would flatten
// them into one.
func refuseScriptFormat(taskID, format string) error {
	f := strings.TrimSpace(format)

	switch {
	case f == "":
		return errs.New(
			errs.M("bpmn: scriptTask %q declares no scriptFormat; there is "+
				"nothing to route the script to, and guessing would run "+
				"another language's syntax through %s",
				taskID, luaScriptFormats[0]),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case classifyScriptFormat(f) == scriptByReference:
		return errs.New(
			errs.M("bpmn: scriptTask %q declares scriptFormat %q, whose script "+
				"text names a Go function the host registered rather than "+
				"carrying the behavior; a document cannot see that code, so "+
				"importing it would admit a task that does nothing until an "+
				"unrelated program registers the matching key",
				taskID, f),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return errs.New(
		errs.M("bpmn: scriptTask %q declares scriptFormat %q, which no script "+
			"engine claims; %s is the supported format. This is a deferral, "+
			"not a verdict on the file — the same task imports once a host "+
			"registers an engine claiming %q",
			taskID, f, strings.Join(luaScriptFormats, " / "), f),
		errs.C(errorClass, errs.InvalidParameter))
}
