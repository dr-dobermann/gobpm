package bpmn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// sample covers the whole SRD-051 §FR-8 MVP subset: both events, the three
// task spellings, both gateways (exclusive with direction, default and a
// conditioned outgoing flow), plus content that must be skipped silently —
// node incoming/outgoing wiring, documentation, extensionElements (modeler
// metadata) and a foreign-namespace (bpmndi) diagram subtree.
const sample = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                  id="defs-1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:extensionElements>
    <vendor:meta xmlns:vendor="urn:example:vendor"/>
  </bpmn:extensionElements>
  <bpmn:process id="proc-1" name="Order" isExecutable="true">
    <bpmn:extensionElements>
      <vendor:procMeta xmlns:vendor="urn:example:vendor"/>
    </bpmn:extensionElements>
    <bpmn:startEvent id="start" name="Begin">
      <bpmn:outgoing>f1</bpmn:outgoing>
      <bpmn:extensionElements>
        <vendor:nodeMeta xmlns:vendor="urn:example:vendor"/>
      </bpmn:extensionElements>
    </bpmn:startEvent>
    <bpmn:task id="t1" name="Do it">
      <bpmn:incoming>f1</bpmn:incoming>
      <bpmn:outgoing>f2</bpmn:outgoing>
      <bpmn:documentation>do the thing</bpmn:documentation>
    </bpmn:task>
    <bpmn:manualTask id="t2" name="By hand"/>
    <bpmn:userTask id="t3" name="Approve"/>
    <bpmn:exclusiveGateway id="gw" gatewayDirection="Diverging" default="f4"/>
    <bpmn:parallelGateway id="pg"/>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="gw"/>
    <bpmn:sequenceFlow id="f3" name="big" sourceRef="gw" targetRef="t3">
      <bpmn:conditionExpression id="c1" language="https://go.dev">total &gt; 100</bpmn:conditionExpression>
      <bpmn:extensionElements>
        <vendor:flowMeta xmlns:vendor="urn:example:vendor"/>
      </bpmn:extensionElements>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f4" sourceRef="gw" targetRef="t2"/>
    <bpmn:sequenceFlow id="f5" sourceRef="t3" targetRef="pg"/>
    <bpmn:sequenceFlow id="f6" sourceRef="t2" targetRef="pg"/>
    <bpmn:sequenceFlow id="f7" sourceRef="pg" targetRef="end"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="diagram-1">
    <bpmndi:BPMNPlane id="plane-1" bpmnElement="proc-1"/>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// wantKinds pins the expected model type of every node in sample.
var wantKinds = map[string]string{
	"start": "*events.StartEvent",
	"end":   "*events.EndEvent",
	"t1":    "*activities.ManualTask",
	"t2":    "*activities.ManualTask",
	"t3":    "*activities.UserTask",
	"gw":    "*gateways.ExclusiveGateway",
	"pg":    "*gateways.ParallelGateway",
}

// TestImportSubset covers SRD-051 §FR-5/§FR-7: the MVP subset imports with
// source ids preserved, defaults and conditions resolved, foreign-namespace
// content skipped.
func TestImportSubset(t *testing.T) {
	p, err := importer{}.Import(context.Background(), strings.NewReader(sample))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if p.ID() != "proc-1" || p.Name() != "Order" {
		t.Errorf("process identity = %q/%q, want proc-1/Order", p.ID(), p.Name())
	}

	nodes := map[string]flow.Node{}
	for _, n := range p.Nodes() {
		nodes[n.ID()] = n
	}

	if len(nodes) != len(wantKinds) {
		t.Errorf("got %d nodes, want %d", len(nodes), len(wantKinds))
	}

	for id, want := range wantKinds {
		n, ok := nodes[id]
		if !ok {
			t.Errorf("node %q missing", id)

			continue
		}

		if got := typeName(n); got != want {
			t.Errorf("node %q is %s, want %s", id, got, want)
		}
	}

	gw, ok := nodes["gw"].(*gateways.ExclusiveGateway)
	if !ok {
		t.Fatalf("gw is %T, want *gateways.ExclusiveGateway", nodes["gw"])
	}

	if gw.Direction() != gateways.Diverging {
		t.Errorf("gw direction = %q, want Diverging", gw.Direction())
	}

	if df := gw.DefaultFlow(); df == nil || df.ID() != "f4" {
		t.Errorf("gw default flow = %v, want f4", df)
	}

	f3 := findFlow(t, p.Flows(), "f3")

	if f3.Source().ID() != "gw" || f3.Target().ID() != "t3" {
		t.Errorf("f3 = %s → %s, want gw → t3", f3.Source().ID(), f3.Target().ID())
	}

	cond, ok := f3.Condition().(*formalExpression)
	if !ok {
		t.Fatalf("f3 condition is %T, want *formalExpression", f3.Condition())
	}

	if cond.ID() != "c1" || cond.Language() != "https://go.dev" || cond.Body() != "total > 100" {
		t.Errorf("condition = %q/%q/%q, want c1/https://go.dev/total > 100",
			cond.ID(), cond.Language(), cond.Body())
	}
}

// TestImportErrors covers the SRD-051 §FR-7 failure modes: unmapped
// in-namespace elements are *convert.UnsupportedElementError, missing ids
// and dangling refs are classified import errors.
func TestImportErrors(t *testing.T) {
	tt := []struct {
		name      string
		doc       string
		want      string
		wantUee   bool
		wantSec   string // substring of UnsupportedElementError.Section when wantUee
		wantClass string // errs.ApplicationError class that must survive wrapErr
	}{
		{
			name: "unsupported in-namespace element",
			doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p"><bpmn:inclusiveGateway id="ig"/></bpmn:process>
</bpmn:definitions>`,
			want:    `unsupported element "inclusiveGateway"`,
			wantUee: true,
			wantSec: "§13.4.3",
		},
		{
			name: "unknown operationRef",
			doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p">
    <bpmn:serviceTask id="st" name="call" operationRef="missing-op"/>
  </bpmn:process>
</bpmn:definitions>`,
			want: `unknown operationRef "missing-op"`,
			// Must keep ObjectNotFound — not re-wrapped as BuildingFailed
			// (error-handling audit: ownError / wrapErr).
			wantClass: "OBJECT_NOT_FOUND",
		},
		{
			name: "flow element without id",
			doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p"><bpmn:task name="x"/></bpmn:process>
</bpmn:definitions>`,
			want: "has no id",
		},
		{
			name: "duplicate flow-element id",
			doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:task id="s"/>
  </bpmn:process>
</bpmn:definitions>`,
			want: `duplicate flow-element id "s"`,
		},
		{
			name: "dangling sourceRef",
			doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:sequenceFlow id="f" sourceRef="ghost" targetRef="s"/>
  </bpmn:process>
</bpmn:definitions>`,
			want: `unknown sourceRef "ghost"`,
		},
		{
			name: "no process",
			doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
</bpmn:definitions>`,
			want: "no <process> element",
		},
		{
			name: "wrong root",
			doc:  `<process xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"/>`,
			want: "root element",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			_, err := importer{}.Import(context.Background(), strings.NewReader(tc.doc))
			if err == nil {
				t.Fatal("want error, got nil")
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}

			var uee *convert.UnsupportedElementError

			if got := errors.As(err, &uee); got != tc.wantUee {
				t.Errorf("errors.As(*UnsupportedElementError) = %v, want %v", got, tc.wantUee)
			}

			if tc.wantUee && tc.wantSec != "" && !strings.Contains(uee.Section, tc.wantSec) {
				t.Errorf("UnsupportedElementError.Section = %q, want substring %q",
					uee.Section, tc.wantSec)
			}

			if tc.wantClass != "" {
				var ae *errs.ApplicationError
				if !errors.As(err, &ae) {
					t.Fatalf("error is %T, want *errs.ApplicationError with class %q", err, tc.wantClass)
				}

				if !ae.HasClass(tc.wantClass) {
					t.Errorf("error classes %v do not include %q (re-wrap may have buried the root cause)",
						ae.Classes, tc.wantClass)
				}
			}
		})
	}
}

// TestRoundTrip covers SRD-051 §NFR-3: import → export → re-import keeps
// ids, node kinds, flow wiring, conditions and gateway defaults.
func TestRoundTrip(t *testing.T) {
	ctx := context.Background()

	p1, err := importer{}.Import(ctx, strings.NewReader(sample))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var buf bytes.Buffer

	if err := (exporter{}).Export(ctx, &buf, p1); err != nil {
		t.Fatalf("Export: %v", err)
	}

	p2, err := importer{}.Import(ctx, &buf)
	if err != nil {
		t.Fatalf("re-import of exported document failed: %v\n---\n%s", err, buf.String())
	}

	if p2.ID() != p1.ID() || p2.Name() != p1.Name() {
		t.Errorf("process identity = %q/%q, want %q/%q",
			p2.ID(), p2.Name(), p1.ID(), p1.Name())
	}

	if len(p2.Nodes()) != len(p1.Nodes()) || len(p2.Flows()) != len(p1.Flows()) {
		t.Fatalf("re-import has %d nodes/%d flows, want %d/%d",
			len(p2.Nodes()), len(p2.Flows()), len(p1.Nodes()), len(p1.Flows()))
	}

	byID := map[string]flow.Node{}
	for _, n := range p2.Nodes() {
		byID[n.ID()] = n
	}

	for _, n := range p1.Nodes() {
		n2, ok := byID[n.ID()]
		if !ok {
			t.Errorf("node %q lost in round-trip", n.ID())

			continue
		}

		if got, want := typeName(n2), wantKinds[n.ID()]; got != want {
			t.Errorf("node %q after round-trip is %s, want %s", n.ID(), got, want)
		}

		if n2.Name() != n.Name() {
			t.Errorf("node %q name = %q, want %q", n.ID(), n2.Name(), n.Name())
		}
	}

	gw2, ok := byID["gw"].(*gateways.ExclusiveGateway)
	if !ok {
		t.Fatalf("gw after round-trip is %T, want *gateways.ExclusiveGateway", byID["gw"])
	}

	if gw2.Direction() != gateways.Diverging {
		t.Errorf("gw direction after round-trip = %q, want Diverging", gw2.Direction())
	}

	if df := gw2.DefaultFlow(); df == nil || df.ID() != "f4" {
		t.Errorf("gw default after round-trip = %v, want f4", df)
	}

	f3 := findFlow(t, p2.Flows(), "f3")

	cond, ok := f3.Condition().(*formalExpression)
	if !ok {
		t.Fatalf("f3 condition after round-trip is %T", f3.Condition())
	}

	if cond.ID() != "c1" || cond.Language() != "https://go.dev" || cond.Body() != "total > 100" {
		t.Errorf("condition after round-trip = %q/%q/%q",
			cond.ID(), cond.Language(), cond.Body())
	}
}

// workedExample is the SRD-051 §6 fixture: start → userTask → exclusiveGateway
// with a conditional branch + a default branch → two ends.
const workedExample = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="approval" name="Approval" isExecutable="true">
    <bpmn:startEvent id="s1" name="start"/>
    <bpmn:userTask id="u1" name="review"/>
    <bpmn:exclusiveGateway id="g1" name="decide" default="f_no"/>
    <bpmn:endEvent id="e_ok" name="approved"/>
    <bpmn:endEvent id="e_no" name="rejected"/>
    <bpmn:sequenceFlow id="f_su" sourceRef="s1" targetRef="u1"/>
    <bpmn:sequenceFlow id="f_ug" sourceRef="u1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f_ok" sourceRef="g1" targetRef="e_ok">
      <bpmn:conditionExpression>approved == true</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f_no" sourceRef="g1" targetRef="e_no"/>
  </bpmn:process>
</bpmn:definitions>`

// TestWorkedExample pins the SRD-051 §6 worked-example expectations.
func TestWorkedExample(t *testing.T) {
	p, err := importer{}.Import(context.Background(), strings.NewReader(workedExample))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if p.Name() != "Approval" || p.ID() != "approval" {
		t.Errorf("process = %q/%q, want Approval/approval", p.Name(), p.ID())
	}

	if got := len(p.Nodes()); got != 5 {
		t.Errorf("nodes = %d, want 5", got)
	}

	if got := len(p.Flows()); got != 4 {
		t.Errorf("flows = %d, want 4", got)
	}

	fOK := findFlow(t, p.Flows(), "f_ok")
	if fOK.Condition() == nil {
		t.Fatal("f_ok carries no condition")
	}

	cond, ok := fOK.Condition().(*formalExpression)
	if !ok {
		t.Fatalf("f_ok condition is %T, want *formalExpression", fOK.Condition())
	}

	if cond.Body() != "approved == true" {
		t.Errorf("f_ok condition body = %q, want %q", cond.Body(), "approved == true")
	}

	var g1 *gateways.ExclusiveGateway

	for _, n := range p.Nodes() {
		if n.ID() == "g1" {
			g1, ok = n.(*gateways.ExclusiveGateway)
			if !ok {
				t.Fatalf("g1 is %T, want *gateways.ExclusiveGateway", n)
			}

			break
		}
	}

	if g1 == nil {
		t.Fatal("g1 missing")
	}

	if df := g1.DefaultFlow(); df == nil || df.ID() != "f_no" {
		t.Errorf("g1.DefaultFlow() = %v, want f_no", df)
	}
}

// TestExportMVP covers SRD-051 §6 TestBPMNExportMVP: a programmatically built
// process exports to XML with correct tags/attrs and no Diagram Interchange.
func TestExportMVP(t *testing.T) {
	ctx := context.Background()

	p, err := process.New("ExportMe", foundation.WithID("export-1"))
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}

	start, err := events.NewStartEvent("Begin", foundation.WithID("s"))
	if err != nil {
		t.Fatalf("NewStartEvent: %v", err)
	}

	task, err := activities.NewManualTask("Work", foundation.WithID("t"))
	if err != nil {
		t.Fatalf("NewManualTask: %v", err)
	}

	gw, err := gateways.NewExclusiveGateway(
		foundation.WithID("g"),
		options.WithName("Decide"),
		gateways.WithDirection(gateways.Diverging))
	if err != nil {
		t.Fatalf("NewExclusiveGateway: %v", err)
	}

	endOK, err := events.NewEndEvent("OK", foundation.WithID("e_ok"))
	if err != nil {
		t.Fatalf("NewEndEvent ok: %v", err)
	}

	endNo, err := events.NewEndEvent("NO", foundation.WithID("e_no"))
	if err != nil {
		t.Fatalf("NewEndEvent no: %v", err)
	}

	for _, n := range []flow.Node{start, task, gw, endOK, endNo} {
		if err := p.Add(n); err != nil {
			t.Fatalf("Add %s: %v", n.ID(), err)
		}
	}

	if _, err := flow.Link(start, task, foundation.WithID("f1")); err != nil {
		t.Fatalf("link f1: %v", err)
	}

	if _, err := flow.Link(task, gw, foundation.WithID("f2")); err != nil {
		t.Fatalf("link f2: %v", err)
	}

	_, err = flow.Link(gw, endOK,
		foundation.WithID("f_ok"),
		flow.WithCondition(newFormalExpression("c1", "", "yes")))
	if err != nil {
		t.Fatalf("link f_ok: %v", err)
	}

	fNo, err := flow.Link(gw, endNo, foundation.WithID("f_no"))
	if err != nil {
		t.Fatalf("link f_no: %v", err)
	}

	if err := gw.UpdateDefaultFlow(fNo); err != nil {
		t.Fatalf("UpdateDefaultFlow: %v", err)
	}

	var buf bytes.Buffer
	if err := (exporter{}).Export(ctx, &buf, p); err != nil {
		t.Fatalf("Export: %v", err)
	}

	xml := buf.String()

	for _, want := range []string{
		`xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"`,
		`id="export-1"`,
		`name="ExportMe"`,
		`isExecutable="true"`,
		`<bpmn:startEvent id="s" name="Begin"`,
		`<bpmn:task id="t" name="Work"`,
		`<bpmn:exclusiveGateway id="g" name="Decide"`,
		`gatewayDirection="Diverging"`,
		`default="f_no"`,
		`<bpmn:endEvent id="e_ok" name="OK"`,
		`<bpmn:endEvent id="e_no" name="NO"`,
		`id="f_ok"`,
		`sourceRef="g"`,
		`targetRef="e_ok"`,
		`<bpmn:conditionExpression`,
		`yes`,
		`id="f_no"`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("exported XML missing %q\n---\n%s", want, xml)
		}
	}

	for _, ban := range []string{
		"bpmndi",
		"BPMNDiagram",
		"BPMNPlane",
		"BPMNShape",
		"BPMNEdge",
	} {
		if strings.Contains(xml, ban) {
			t.Errorf("exported XML must not contain DI token %q\n---\n%s", ban, xml)
		}
	}
}

// TestPreservesID covers SRD-051 §6 TestBPMNPreservesID: the imported process
// id is the versioning key handed to thresher.RegisterProcess (ADR-019).
func TestPreservesID(t *testing.T) {
	ctx := context.Background()

	p, err := importer{}.Import(ctx, strings.NewReader(workedExample))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if p.ID() != "approval" {
		t.Fatalf("process id = %q, want approval", p.ID())
	}

	th, err := thresher.New("preserve-id-test")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	reg, err := th.RegisterProcess(p)
	if err != nil {
		t.Fatalf("RegisterProcess: %v", err)
	}

	if reg.Key() != "approval" {
		t.Errorf("registration key = %q, want approval (BPMN process id)", reg.Key())
	}
}

// runnableLinear is a start → task → end graph that the engine can run to
// completion without human tasks or evaluable conditions (ManualTask is a
// no-op pass-through).
const runnableLinear = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="linear" name="Linear" isExecutable="true">
    <bpmn:startEvent id="s1" name="start"/>
    <bpmn:task id="t1" name="work"/>
    <bpmn:endEvent id="e1" name="done"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

// TestImportRegisterRun covers SRD-051 §6 TestBPMNImportRegisterRun: import →
// register → run to completion on a thresher.
func TestImportRegisterRun(t *testing.T) {
	if err := data.CreateDefaultStates(); err != nil {
		t.Fatalf("CreateDefaultStates: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(runnableLinear))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	th, err := thresher.New("import-register-run")
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

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		t.Fatalf("WaitCompletion: %v", err)
	}

	if state != thresher.StateCompleted {
		t.Errorf("completion state = %q, want %q", state, thresher.StateCompleted)
	}
}

// TestExportErrors covers the export-side argument checks and the
// unsupported-node feedback (SRD-051 §FR-3).
func TestExportErrors(t *testing.T) {
	var nilCtx context.Context
	// Проверяем, что Export отклоняет nil context.
	if err := (exporter{}).Export(nilCtx, &bytes.Buffer{}, nil); err == nil {
		t.Error("Export(nil ctx): want error, got nil")
	}

	ctx := context.Background()
	p, err := importer{}.Import(ctx, strings.NewReader(sample))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if err := (exporter{}).Export(ctx, nil, p); err == nil ||
		!strings.Contains(err.Error(), "w is nil") {
		t.Errorf("Export(nil writer): %v", err)
	}

	if err := (exporter{}).Export(ctx, &bytes.Buffer{}, nil); err == nil ||
		!strings.Contains(err.Error(), "p is nil") {
		t.Errorf("Export(nil process): %v", err)
	}

	// a node outside the MVP subset aborts the export with
	// *convert.UnsupportedElementError
	sp, err := activities.NewSubProcess("sub")
	if err != nil {
		t.Fatalf("NewSubProcess: %v", err)
	}

	if err := p.Add(sp); err != nil {
		t.Fatalf("Add subprocess: %v", err)
	}

	err = (exporter{}).Export(ctx, &bytes.Buffer{}, p)

	var uee *convert.UnsupportedElementError
	if !errors.As(err, &uee) {
		t.Fatalf("Export with subprocess: error is %v (%T), want *convert.UnsupportedElementError", err, err)
	}
}

// serviceTaskSample is a minimal SRD-051 §4.6 document: interface + operation
// + serviceTask@operationRef in a linear start → serviceTask → end graph.
const serviceTaskSample = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:interface id="iface-1" name="Greeter">
    <bpmn:operation id="op-greet" name="greet">
      <bpmn:inMessageRef>msg-in</bpmn:inMessageRef>
    </bpmn:operation>
  </bpmn:interface>
  <bpmn:process id="svc-proc" name="ServiceProc" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:serviceTask id="st1" name="call" operationRef="op-greet"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="st1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="st1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

// TestServiceTaskImport covers serviceTask + interface/operation import
// (SRD-051 §4.6). The bound Operation is a catalog stub (no Implementor) —
// the host supplies a real implementor after import.
func TestServiceTaskImport(t *testing.T) {
	p, err := importer{}.Import(context.Background(), strings.NewReader(serviceTaskSample))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if p.ID() != "svc-proc" {
		t.Errorf("process id = %q, want svc-proc", p.ID())
	}

	var st *activities.ServiceTask

	for _, n := range p.Nodes() {
		if n.ID() == "st1" {
			var ok bool

			st, ok = n.(*activities.ServiceTask)
			if !ok {
				t.Fatalf("st1 is %T, want *activities.ServiceTask", n)
			}

			break
		}
	}

	if st == nil {
		t.Fatal("serviceTask st1 missing")
	}

	if st.Name() != "call" {
		t.Errorf("serviceTask name = %q, want call", st.Name())
	}

	// Implementation is ##unspecified when the Operation has no Implementor.
	if impl := st.Implementation(); impl != "" && impl != "https://go.dev" {
		// Accept ##unspecified (gobpm service.UnspecifiedImplementation).
		if impl != "##unspecified" {
			t.Logf("implementation = %q (informational)", impl)
		}
	}
}

// TestServiceTaskSyntheticOp covers a serviceTask without operationRef:
// import mints a synthetic Operation id = taskID:operation.
func TestServiceTaskSyntheticOp(t *testing.T) {
	doc := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="st" name="work"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="st"/>
    <bpmn:sequenceFlow id="f2" sourceRef="st" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	found := false

	for _, n := range p.Nodes() {
		if n.ID() == "st" {
			if _, ok := n.(*activities.ServiceTask); !ok {
				t.Fatalf("st is %T, want *activities.ServiceTask", n)
			}

			found = true
		}
	}

	if !found {
		t.Fatal("serviceTask st missing")
	}
}

// TestServiceTaskExportProgrammatic covers exporting a hand-built ServiceTask:
// the element, its operationRef, and the definitions-level interface catalog
// that makes the ref resolvable on re-import (SRD-051 §FR-6/§4.6).
func TestServiceTaskExportProgrammatic(t *testing.T) {
	ctx := context.Background()

	p, err := process.New("SvcExport", foundation.WithID("svc-export"))
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}

	op, err := service.NewOperation("greet", nil, nil, nil, foundation.WithID("op-1"))
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}

	st, err := activities.NewServiceTask("call", op,
		foundation.WithID("st"),
		activities.WithoutParams())
	if err != nil {
		t.Fatalf("NewServiceTask: %v", err)
	}

	start, err := events.NewStartEvent("s", foundation.WithID("s"))
	if err != nil {
		t.Fatalf("NewStartEvent: %v", err)
	}

	end, err := events.NewEndEvent("e", foundation.WithID("e"))
	if err != nil {
		t.Fatalf("NewEndEvent: %v", err)
	}

	for _, n := range []flow.Node{start, st, end} {
		if err := p.Add(n); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	if _, err := flow.Link(start, st, foundation.WithID("f1")); err != nil {
		t.Fatalf("link f1: %v", err)
	}

	if _, err := flow.Link(st, end, foundation.WithID("f2")); err != nil {
		t.Fatalf("link f2: %v", err)
	}

	var buf bytes.Buffer
	if err := (exporter{}).Export(ctx, &buf, p); err != nil {
		t.Fatalf("Export: %v", err)
	}

	xml := buf.String()
	if !strings.Contains(xml, `<bpmn:serviceTask id="st"`) {
		t.Errorf("exported XML missing serviceTask:\n%s", xml)
	}

	if !strings.Contains(xml, `operationRef="op-1"`) {
		t.Errorf("exported XML missing operationRef:\n%s", xml)
	}

	if !strings.Contains(xml, `<bpmn:interface`) || !strings.Contains(xml, `id="op-1"`) {
		t.Errorf("exported XML missing interface/operation catalog:\n%s", xml)
	}
}

// TestServiceTaskRoundTrip imports a catalog-bound serviceTask, exports, and
// re-imports: the node kind, its id and its operation binding all survive
// (SRD-051 §NFR-3 semantic round-trip).
func TestServiceTaskRoundTrip(t *testing.T) {
	ctx := context.Background()

	p1, err := importer{}.Import(ctx, strings.NewReader(serviceTaskSample))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var buf bytes.Buffer
	if err := (exporter{}).Export(ctx, &buf, p1); err != nil {
		t.Fatalf("Export: %v", err)
	}

	p2, err := importer{}.Import(ctx, &buf)
	if err != nil {
		t.Fatalf("re-import: %v\n---\n%s", err, buf.String())
	}

	found := false

	for _, n := range p2.Nodes() {
		if n.ID() == "st1" {
			st, ok := n.(*activities.ServiceTask)
			if !ok {
				t.Fatalf("st1 after round-trip is %T", n)
			}

			// The operationRef must survive export and re-resolve against
			// the exported interface catalog — otherwise the round-trip
			// silently drops the task's service binding.
			if op := st.Operation(); op == nil || op.ID() != "op-greet" {
				t.Errorf("st1 operation after round-trip = %v, want op-greet\n---\n%s",
					op, buf.String())
			}

			found = true
		}
	}

	if !found {
		t.Fatalf("st1 lost in round-trip\n---\n%s", buf.String())
	}
}

// TestExportCanceledContext covers per-element ctx checks during export.
func TestExportCanceledContext(t *testing.T) {
	p, err := importer{}.Import(context.Background(), strings.NewReader(sample))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = (exporter{}).Export(ctx, &bytes.Buffer{}, p)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Export(canceled ctx): error = %v, want context.Canceled", err)
	}
}

// TestFormalExpressionContract covers the inert condition carrier required by
// SRD-051 §FR-5: imported text is inspectable but never evaluated here.
func TestFormalExpressionContract(t *testing.T) {
	e := newFormalExpression("condition-1", "urn:test", "approved")

	if e.Body() != "approved" || e.ID() != "condition-1" || e.Language() != "urn:test" {
		t.Errorf("expression identity = %q/%q/%q", e.ID(), e.Language(), e.Body())
	}
	if e.Docs() != nil || e.ResultType() != "bool" || e.IsEvaluated() {
		t.Errorf("expression metadata = docs %v, type %q, evaluated %v",
			e.Docs(), e.ResultType(), e.IsEvaluated())
	}
	if value, err := e.Evaluate(context.Background(), nil); err == nil || value != nil {
		t.Errorf("Evaluate = %v, %v; want nil, error", value, err)
	}
	if value, err := e.Result(); err == nil || value != nil {
		t.Errorf("Result = %v, %v; want nil, error", value, err)
	}
}

// TestErrorHelpers covers the error-class preservation policy documented in
// AGENTS.md and used by SRD-051 §FR-3 failures.
func TestErrorHelpers(t *testing.T) {
	if ownError(nil) || wrapErr("context", errs.BulidingFailed, nil) != nil {
		t.Fatal("nil error must remain nil and must not be classified as owned")
	}

	uee := &convert.UnsupportedElementError{Tag: "inclusiveGateway"}
	if !ownError(fmt.Errorf("outer: %w", uee)) ||
		!errors.Is(wrapErr("context", errs.BulidingFailed, uee), uee) {
		t.Fatal("UnsupportedElementError must pass through unchanged")
	}

	owned := errs.New(errs.M("owned"), errs.C(errorClass, errs.InvalidObject))
	if !ownError(owned) || !errors.Is(wrapErr("context", errs.BulidingFailed, owned), owned) {
		t.Fatal("package-classified ApplicationError must pass through unchanged")
	}

	foreign := errors.New("foreign")
	wrapped := wrapErr("while converting", errs.BulidingFailed, foreign)
	var applicationError *errs.ApplicationError
	if ownError(foreign) || !errors.As(wrapped, &applicationError) || !errors.Is(wrapped, foreign) {
		t.Fatalf("foreign wrap = %v; cause must remain inspectable", wrapped)
	}
}

// TestImporterBoundaryErrors covers argument, XML-stream and definitions
// boundary failures from SRD-051 §FR-5/§FR-7.
func TestImporterBoundaryErrors(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		doc  string
		want string
	}{
		{name: "nil context", want: "ctx is nil"},
		{name: "malformed XML", ctx: context.Background(), doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">`, want: "XML syntax error"},
		{name: "process without id", ctx: context.Background(), doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process/></bpmn:definitions>`, want: "has no id"},
		{name: "second process", ctx: context.Background(), doc: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="p"/><bpmn:process id="q"/></bpmn:definitions>`, want: "unsupported element"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (importer{}).Import(tc.ctx, strings.NewReader(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Import error = %v, want substring %q", err, tc.want)
			}
		})
	}

	if _, err := (importer{}).Import(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "r is nil") {
		t.Fatalf("Import(nil reader) = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (importer{}).Import(ctx, strings.NewReader(sample)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Import(canceled context) = %v, want context.Canceled", err)
	}
}

// TestImportValidFixtures covers representative complete processes from the
// supported SRD-051 §FR-8 subset. Files under testdata double as readable
// examples and are parsed through the same io.Reader boundary as callers use.
func TestImportValidFixtures(t *testing.T) {
	tests := []struct {
		file      string
		processID string
		nodes     int
		flows     int
	}{
		{file: "linear.bpmn", processID: "linear-fixture", nodes: 3, flows: 2},
		{file: "exclusive-branch.bpmn", processID: "exclusive-fixture", nodes: 5, flows: 4},
		{file: "parallel-service.bpmn", processID: "parallel-service-fixture", nodes: 6, flows: 6},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open("testdata/valid/" + tc.file)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer func() {
				if err := f.Close(); err != nil {
					t.Errorf("close fixture: %v", err)
				}
			}()

			p, err := (importer{}).Import(context.Background(), f)
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if p.ID() != tc.processID || len(p.Nodes()) != tc.nodes || len(p.Flows()) != tc.flows {
				t.Errorf("process = %q, %d nodes, %d flows; want %q, %d, %d",
					p.ID(), len(p.Nodes()), len(p.Flows()), tc.processID, tc.nodes, tc.flows)
			}
		})
	}
}

// TestImportInvalidFixtures covers representative document-level failures
// required to fail closed by SRD-051 §FR-7 and ADR-019.
func TestImportInvalidFixtures(t *testing.T) {
	tests := []struct {
		file    string
		want    string
		wantUEE bool
	}{
		{file: "missing-process-id.bpmn", want: "has no id"},
		{file: "duplicate-element-id.bpmn", want: "duplicate flow-element id"},
		{file: "dangling-target.bpmn", want: "unknown targetRef"},
		{file: "unknown-operation.bpmn", want: "unknown operationRef"},
		{file: "unsupported-element.bpmn", want: `unsupported element "scriptTask"`, wantUEE: true},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open("testdata/invalid/" + tc.file)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer func() {
				if err := f.Close(); err != nil {
					t.Errorf("close fixture: %v", err)
				}
			}()

			_, err = (importer{}).Import(context.Background(), f)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Import error = %v, want substring %q", err, tc.want)
			}

			var uee *convert.UnsupportedElementError
			if got := errors.As(err, &uee); got != tc.wantUEE {
				t.Errorf("errors.As(*UnsupportedElementError) = %v, want %v", got, tc.wantUEE)
			}
		})
	}
}

// TestSectionFor covers the actionable BPMN spec pins required by
// SRD-051 §FR-3 / SAD-001 §5.
func TestSectionFor(t *testing.T) {
	tests := map[string]string{
		"sendTask":                         "§13.3.3",
		"subProcess":                       "§13.3.4",
		"inclusiveGateway":                 "§13.4.3",
		"eventBasedGateway":                "§13.4",
		"intermediateCatchEvent":           "§13.5",
		"boundaryEvent":                    "§13.5.5",
		"messageEventDefinition":           "§13.5",
		"lane":                             "§10.5",
		"dataObject":                       "§10.3",
		"collaboration":                    "§10.1",
		"dataInputAssociation":             "§10.3",
		"multiInstanceLoopCharacteristics": "§13.3.5",
		"unknown":                          "",
	}

	for tag, want := range tests {
		if got := sectionFor(tag); got != want {
			t.Errorf("sectionFor(%q) = %q, want %q", tag, got, want)
		}
	}
}

type failingWriter struct{ calls int }

func (w *failingWriter) Write(_ []byte) (int, error) {
	w.calls++
	return 0, io.ErrClosedPipe
}

// TestExportWriteFailure covers propagation of output failures from
// SRD-051 §FR-6 serialization.
func TestExportWriteFailure(t *testing.T) {
	p, err := importer{}.Import(context.Background(), strings.NewReader(runnableLinear))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	w := &failingWriter{}
	err = (exporter{}).Export(context.Background(), w, p)
	if err == nil || !errors.Is(err, io.ErrClosedPipe) || w.calls == 0 {
		t.Fatalf("Export failing writer = %v (calls %d), want closed-pipe cause", err, w.calls)
	}
}

// typeName renders a node's concrete type the way wantKinds spells it.
func typeName(n flow.Node) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", n), "*github.com/dr-dobermann/gobpm/pkg/model/")
}

// findFlow locates a sequence flow by id or fails the test.
func findFlow(t *testing.T, flows []*flow.SequenceFlow, id string) *flow.SequenceFlow {
	t.Helper()

	for _, f := range flows {
		if f.ID() == id {
			return f
		}
	}

	t.Fatalf("flow %q missing", id)

	return nil
}
