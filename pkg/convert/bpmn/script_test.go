package bpmn

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestClassifyScriptFormat covers the three outcomes a declared format can
// have — and they are three, not two, because "no engine claims this" and
// "an engine claims it but the text names host code" are different
// problems with different remedies.
func TestClassifyScriptFormat(t *testing.T) {
	tests := map[string]scriptFormatVerdict{
		"lua":                        scriptLua,
		"text/x-lua":                 scriptLua,
		"application/x-lua":          scriptLua,
		"  LUA  ":                    scriptLua, // trimmed and case-folded
		"gofunc":                     scriptByReference,
		"application/x-gobpm-gofunc": scriptByReference,
		"javascript":                 scriptRefused,
		"groovy":                     scriptRefused,
		"python":                     scriptRefused,
		"":                           scriptRefused,
	}

	for format, want := range tests {
		t.Run(format, func(t *testing.T) {
			if got := classifyScriptFormat(format); got != want {
				t.Errorf("classifyScriptFormat(%q) = %d, want %d", format, got, want)
			}
		})
	}
}

// TestRefuseScriptFormat pins that each refusal explains ITS problem. A
// modeller reading "unsupported format" learns nothing; reading that the
// format is unclaimed learns to register an engine, and reading that the
// script names host code learns the file was never self-contained.
func TestRefuseScriptFormat(t *testing.T) {
	tests := map[string]struct {
		format string
		names  []string
	}{
		"an absent format says what guessing would cost": {
			"", []string{"no scriptFormat", "guessing"},
		},
		"a by-reference format says where the behaviour lives": {
			"gofunc", []string{"names a Go function", "host registered"},
		},
		"an unclaimed format says it is a deferral": {
			"javascript", []string{"no script engine claims", "deferral"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := refuseScriptFormat("t1", tc.format)
			if err == nil {
				t.Fatal("refuseScriptFormat returned nil")
			}

			if !strings.Contains(err.Error(), `"t1"`) {
				t.Errorf("refusal %q does not name the task", err)
			}

			for _, want := range tc.names {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestLuaFormatsMatchTheBattery guards a hazard the module layout creates.
//
// adapters/lua is a separate module the root module does not require, so
// the converter cannot import the battery to ask what it claims — the
// three format strings are copied. If the battery ever adds or renames
// one, a perfectly valid script task would be refused with no test failing
// anywhere, because nothing connects the two lists.
//
// This reads the adapter's source as a FILE, which crosses no module
// boundary — the same technique internal/lintcfg uses to hold the
// observability vocabulary together across files it cannot import.
func TestLuaFormatsMatchTheBattery(t *testing.T) {
	root := repoRoot(t)

	src, err := os.ReadFile(filepath.Join(root, "adapters", "lua", "engine.go"))
	if err != nil {
		t.Fatalf("reading the Lua battery: %v", err)
	}

	// var formats = []string{"text/x-lua", "application/x-lua", "lua"}
	decl := regexp.MustCompile(`var formats = \[\]string\{([^}]*)\}`).
		FindSubmatch(src)
	if decl == nil {
		t.Fatal("the Lua battery no longer declares `var formats = []string{…}`; " +
			"this guard needs updating along with whatever replaced it")
	}

	claimed := regexp.MustCompile(`"([^"]+)"`).
		FindAllStringSubmatch(string(decl[1]), -1)

	got := make([]string, 0, len(claimed))
	for _, m := range claimed {
		got = append(got, m[1])
	}

	if strings.Join(got, ",") != strings.Join(luaScriptFormats, ",") {
		t.Errorf("the Lua battery claims %v but the converter accepts %v; "+
			"they must agree, or a valid script task is refused for a reason "+
			"no test would show", got, luaScriptFormats)
	}
}

// repoRoot walks up from the test's directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "adapters")); err == nil {
				return dir
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no module root with an adapters/ directory above the test")
		}

		dir = parent
	}
}
