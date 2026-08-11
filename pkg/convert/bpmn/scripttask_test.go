package bpmn

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
)

// scriptDoc is a linear process whose one task is the script under test.
func scriptDoc(attrs, body string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:scriptTask id="t1" name="Compute" %s>%s</bpmn:scriptTask>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, attrs, body)
}

// TestImportScriptTask covers SRD-089.C §FR-1: a Lua script task imports
// with its format and body intact.
func TestImportScriptTask(t *testing.T) {
	const src = `data.set("total", 42)`

	for _, format := range []string{"lua", "text/x-lua", "application/x-lua"} {
		t.Run(format, func(t *testing.T) {
			doc := scriptDoc(
				fmt.Sprintf("scriptFormat=%q", format),
				"<bpmn:script>"+src+"</bpmn:script>")

			p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
			if err != nil {
				t.Fatalf("Import: %v", err)
			}

			var st *activities.ScriptTask

			for _, n := range p.Nodes() {
				if s, ok := n.(*activities.ScriptTask); ok {
					st = s
				}
			}

			if st == nil {
				t.Fatal("no script task after import")
			}

			if st.ScriptFormat() != format {
				t.Errorf("ScriptFormat() = %q, want %q", st.ScriptFormat(), format)
			}

			if st.ID() != "t1" || st.Name() != "Compute" {
				t.Errorf("script task identity = %q/%q, want t1/Compute",
					st.ID(), st.Name())
			}
		})
	}
}

// TestScriptTaskRefusals covers the refusal set, and the ORDER in which
// the two checks happen: a file written in another language is told about
// the language, not about a missing script.
func TestScriptTaskRefusals(t *testing.T) {
	const body = "<bpmn:script>x = 1</bpmn:script>"

	tests := map[string]struct {
		attrs, body string
		names       string
	}{
		"a format no engine claims": {
			`scriptFormat="javascript"`, body, "no script engine claims",
		},
		"a by-reference format": {
			`scriptFormat="gofunc"`, body, "names a Go function",
		},
		"no format at all": {
			"", body, "no scriptFormat",
		},
		"a format but no script": {
			`scriptFormat="lua"`, "", "carries no <script>",
		},
		"the language is reported before the missing body": {
			// Both wrong at once: the answer names the LANGUAGE, because
			// fixing the body would not make this file run.
			`scriptFormat="javascript"`, "", "no script engine claims",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importer{}.Import(context.Background(),
				strings.NewReader(scriptDoc(tc.attrs, tc.body)))
			if err == nil {
				t.Fatal("Import: want a refusal")
			}

			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("refusal %q does not say %q", err, tc.names)
			}

			if !strings.Contains(err.Error(), `"t1"`) {
				t.Errorf("refusal %q does not name the task", err)
			}
		})
	}
}

// TestScriptBodyIsReadVerbatim pins that the body arrives as written.
// A script is data to the converter — it neither parses nor rewrites it,
// so entity-escaped operators must survive exactly.
func TestScriptBodyIsReadVerbatim(t *testing.T) {
	doc := scriptDoc(`scriptFormat="lua"`,
		`<bpmn:script>if a &lt; b and c &gt; d then return 1 end</bpmn:script>`)

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, n := range p.Nodes() {
		st, ok := n.(*activities.ScriptTask)
		if !ok {
			continue
		}

		want := "if a < b and c > d then return 1 end"
		if got := st.Script(); got != want {
			t.Errorf("script = %q, want %q", got, want)
		}
	}
}

// TestScriptBodyStreamFailure covers the body reader's error path: a
// document that ends inside <script> must fail the import rather than
// build a task with a truncated program.
func TestScriptBodyStreamFailure(t *testing.T) {
	_, err := importer{}.Import(context.Background(), strings.NewReader(
		`<?xml version="1.0"?><bpmn:definitions xmlns:bpmn="`+nsBPMN+`">`+
			`<bpmn:process id="P" name="P">`+
			`<bpmn:scriptTask id="t1" scriptFormat="lua"><bpmn:script>x = 1`))

	if err == nil {
		t.Fatal("a truncated <script> must fail the import")
	}
}
