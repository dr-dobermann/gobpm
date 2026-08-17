package bpmn

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// TestTranslateJUEL covers ADR-024 §2.10's table verbatim, plus the
// cases a rewriter gets wrong when it is written as a search-and-replace.
func TestTranslateJUEL(t *testing.T) {
	tests := map[string]struct{ in, want string }{
		// The ADR's seven rows.
		"delimiters stripped":     {`${total > 100}`, `total > 100`},
		"&& becomes and":          {`${total > 100 && tier == "gold"}`, `total > 100 and tier == "gold"`},
		"! and || become words":   {`${!approved || blocked}`, `not approved or blocked`},
		"member access unchanged": {`${order.customer.tier == 'vip'}`, `order.customer.tier == 'vip'`},
		"index access unchanged":  {`${items[0] == "sku-1"}`, `items[0] == "sku-1"`},
		"the variable idiom collapses": {
			`${execution.getVariable("total") > 0}`, `total > 0`,
		},

		// The #{…} spelling is the same language.
		"hash delimiters": {`#{approved}`, `approved`},

		// What a search-and-replace breaks.
		"operators inside a string are data": {
			`${name == "a && b"}`, `name == "a && b"`,
		},
		"a word inside a string is data": {
			`${label == "not empty"}`, `label == "not empty"`,
		},
		"!= is not negation": {`${a != b}`, `a != b`},
		"!= and ! together":  {`${!ok && a != b}`, `not ok and a != b`},
		"null becomes nil":   {`${x == null}`, `x == nil`},

		// The builtins the target grammar shares survive as calls.
		"len survives":  {`${len(items) > 0}`, `len(items) > 0`},
		"has survives":  {`${has('rates')}`, `has('rates')`},
		"time survives": {`${deadline > time("2026-12-31T00:00:00Z")}`, `deadline > time("2026-12-31T00:00:00Z")`},

		"single-quoted variable name": {
			`${execution.getVariable('tier') == "vip"}`, `tier == "vip"`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := translateJUEL(tc.in)
			if err != nil {
				t.Fatalf("translateJUEL(%q): %v", tc.in, err)
			}

			if got != tc.want {
				t.Errorf("translateJUEL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestTranslateJUELRefusals covers the other half of the contract, which
// matters more than the translations: everything the target grammar cannot
// express is refused BY NAME. An approximation here yields a condition that
// parses, evaluates, and routes the token the wrong way.
func TestTranslateJUELRefusals(t *testing.T) {
	tests := map[string]struct{ in, names string }{
		"a bean method":          {`${myBean.check(order)}`, "myBean.check"},
		"a function namespace":   {`${fn:length(items) > 0}`, "fn"},
		"the ternary operator":   {`${a ? b : c}`, "ternary"},
		"the empty operator":     {`${empty items}`, "empty"},
		"a word-form comparison": {`${a gt b}`, "word form"},
		"word-form division":     {`${a div b > 1}`, "word form"},
		"execution beyond getVariable": {
			`${execution.getProcessInstanceId() == "x"}`, "execution",
		},
		"a computed variable name": {
			`${execution.getVariable(key)}`, "non-literal",
		},
		"a string template rather than an expression": {
			`${a} and ${b}`, "template",
		},
		"an unterminated literal": {`${name == "oops}`, "unterminated"},
		"no delimiters at all":    {`total > 100`, "delimiters"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := translateJUEL(tc.in)
			if err == nil {
				t.Fatalf("translateJUEL(%q) = %q, want a refusal", tc.in, got)
			}

			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("refusal %q does not name %q", err, tc.names)
			}

			// Every refusal quotes the expression it came from, so a file
			// with several conditions says which one stopped the import.
			// strconv.Quote, because the message renders it with %q and a
			// raw comparison would miss the escaped inner quotes.
			if !strings.Contains(err.Error(), strconv.Quote(strings.TrimSpace(tc.in))) {
				t.Errorf("refusal %q does not quote the expression", err)
			}
		})
	}
}

// TestJUELDetection pins the syntactic tell, which is how a Camunda file —
// which labels its expressions with no language at all — is recognized.
func TestJUELDetection(t *testing.T) {
	jueled := []string{`${a}`, `#{a}`, `  ${a > 1}  `}
	plain := []string{`a > 1`, `${a`, `a}`, ``, `$a`}

	for _, s := range jueled {
		if !isJUEL(s) {
			t.Errorf("isJUEL(%q) = false, want true", s)
		}
	}

	for _, s := range plain {
		if isJUEL(s) {
			t.Errorf("isJUEL(%q) = true, want false", s)
		}
	}
}

// TestTranslatedJUELIsRunnable closes the loop the unit tests above cannot.
// They compare the translator's output to a string; this one hands that
// output to the engine and watches it evaluate.
//
// A rewrite can be textually plausible and still not parse — or parse and
// mean something else — and only the target engine can say. This is the
// same two-flow gateway M2's proof uses, for the same reason: with one
// outgoing flow the gateway never consults the condition at all.
func TestTranslatedJUELIsRunnable(t *testing.T) {
	if err := data.CreateDefaultStates(); err != nil {
		t.Fatalf("CreateDefaultStates: %v", err)
	}

	// No language declared anywhere — exactly what a Camunda file carries.
	// The ${…} delimiters are the only evidence, which is the point.
	doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:process id="juel-run" name="juel-run" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:exclusiveGateway id="g1" default="f3"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1">
      <bpmn:conditionExpression>${!(1 &gt; 2) &amp;&amp; ghost == null}</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN)

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import of an undeclared JUEL condition: %v", err)
	}

	// The stored condition is lite source, not the JUEL it arrived as.
	for _, f := range p.Flows() {
		if f.ID() != "f2" {
			continue
		}

		te, ok := f.Condition().(*data.TextExpression)
		if !ok {
			t.Fatalf("condition is %T, want the model's text kind", f.Condition())
		}

		if te.Language() != lite.Language {
			t.Errorf("condition language = %q, want %q", te.Language(), lite.Language)
		}

		if strings.Contains(te.Body(), "${") || strings.Contains(te.Body(), "&&") {
			t.Errorf("condition body %q still carries JUEL syntax", te.Body())
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	th, err := thresher.New("translated-juel")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	if _, err := th.RegisterProcess(p); err != nil {
		t.Fatalf("RegisterProcess: %v", err)
	}

	if err := th.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := th.StartLatest(p.ID())
	if err != nil {
		t.Fatalf("StartLatest: %v", err)
	}

	// "ghost" is undefined, so a translation the engine can PARSE fails at
	// the read. A translation it cannot parse fails differently, and a
	// condition never evaluated would take the default and complete — so
	// completing cleanly is the one outcome that would mean the rewrite
	// never reached the engine.
	state, err := h.WaitCompletion(ctx)
	if err == nil && state == thresher.StateCompleted {
		t.Errorf("state = %q with no error — the translated condition was "+
			"never evaluated", state)
	}
}

// TestTranslateJUELEdges covers the scanner's remaining branches — each a
// real input, none contrived for coverage.
func TestTranslateJUELEdges(t *testing.T) {
	t.Run("an escaped quote inside a literal", func(t *testing.T) {
		got, err := translateJUEL(`${name == "it\"s"}`)
		if err != nil {
			t.Fatalf("translateJUEL: %v", err)
		}

		if got != `name == "it\"s"` {
			t.Errorf("got %q, want the literal intact", got)
		}
	})

	t.Run("a bitwise operator is refused", func(t *testing.T) {
		// A single & or | is JUEL-legal and has no counterpart: the target
		// grammar has boolean operators, not bitwise ones.
		if _, err := translateJUEL(`${a & b}`); err == nil ||
			!strings.Contains(err.Error(), "bitwise") {
			t.Errorf("translateJUEL(${a & b}) error = %v, want a bitwise refusal", err)
		}
	})

	t.Run("getVariable without a call is refused", func(t *testing.T) {
		if _, err := translateJUEL(`${execution.getVariable}`); err == nil ||
			!strings.Contains(err.Error(), "getVariable") {
			t.Errorf("error = %v, want a refusal naming getVariable", err)
		}
	})
}

// TestDelimitersOutrankADeclaredLanguage pins a deliberate decision: a
// document that DECLARES XPath and WRITES ${…} is a Camunda file whose
// schema default nobody edited. Refusing it on its own mislabelling would
// help no one, so the delimiters win.
func TestDelimitersOutrankADeclaredLanguage(t *testing.T) {
	doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s" expressionLanguage="%s">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:exclusiveGateway id="g1" default="f3"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1">
      <bpmn:conditionExpression>${approved}</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, nsXPath)

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, f := range p.Flows() {
		if f.ID() == "f2" && f.Condition().Language() != lite.Language {
			t.Errorf("condition language = %q, want %q — the ${…} delimiters "+
				"outrank the document's declared XPath",
				f.Condition().Language(), lite.Language)
		}
	}
}

// TestUntranslatableJUELRefusesTheImport closes the path from a bad
// expression to a refused FILE: the translator's error must reach the
// caller of Import, not be swallowed into a condition-less flow.
func TestUntranslatableJUELRefusesTheImport(t *testing.T) {
	doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:exclusiveGateway id="g1" default="f3"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1">
      <bpmn:conditionExpression>${myBean.check(order)}</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN)

	_, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err == nil {
		t.Fatal("Import: a bean call must refuse the file, not import silently")
	}

	if !strings.Contains(err.Error(), "myBean.check") {
		t.Errorf("error %q does not name the construct that stopped it", err)
	}
}
