package observability

import "testing"

type capturedLog struct {
	level string
	msg   string
	args  []any
}

// captureLogger records every leveled call so a test can assert the echo's
// level, message and key/value args.
type captureLogger struct {
	records []capturedLog
}

func (c *captureLogger) Debug(msg string, args ...any) { c.put("DEBUG", msg, args) }
func (c *captureLogger) Info(msg string, args ...any)  { c.put("INFO", msg, args) }
func (c *captureLogger) Warn(msg string, args ...any)  { c.put("WARN", msg, args) }
func (c *captureLogger) Error(msg string, args ...any) { c.put("ERROR", msg, args) }

func (c *captureLogger) put(level, msg string, args []any) {
	c.records = append(c.records, capturedLog{level, msg, args})
}

// argMap folds the flat key/value args into a map for assertion.
func argMap(args []any) map[string]string {
	m := make(map[string]string, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		k, _ := args[i].(string)
		v, _ := args[i+1].(string)
		m[k] = v
	}

	return m
}

func TestEchoWritesOneRecordAtMappedLevel(t *testing.T) {
	tests := []struct {
		name      string
		ev        Fact
		wantLevel string
		wantMsg   string
	}{
		{"lifecycle info", Fact{Kind: KindEngineState, Phase: PhaseStarted}, "INFO", "EngineState Started"},
		{"flow debug", Fact{Kind: KindNodeProgress, Phase: PhaseEntered}, "DEBUG", "NodeProgress Entered"},
		{"job warn", Fact{Kind: KindJobState, Phase: PhaseRetriesExhausted}, "WARN", "JobState RetriesExhausted"},
		{"fault error", Fact{Kind: KindFault, Phase: PhaseUncaught}, "ERROR", "Fault Uncaught"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := &captureLogger{}
			Echo(cl, tt.ev)

			if len(cl.records) != 1 {
				t.Fatalf("got %d records, want 1", len(cl.records))
			}

			r := cl.records[0]
			if r.level != tt.wantLevel || r.msg != tt.wantMsg {
				t.Errorf("got %s %q, want %s %q",
					r.level, r.msg, tt.wantLevel, tt.wantMsg)
			}
		})
	}
}

func TestEchoFlattensIdentityAndDetails(t *testing.T) {
	cl := &captureLogger{}
	Echo(cl, Fact{
		Kind: KindJobState, Phase: PhaseRetriesExhausted,
		NodeID: "n1", NodeName: "send",
		Details: map[string]string{AttrJobID: "j1", AttrTopic: "orders"},
	})

	got := argMap(cl.records[0].args)
	want := map[string]string{
		AttrNodeID: "n1", AttrNodeName: "send",
		AttrJobID: "j1", AttrTopic: "orders",
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("arg %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestEchoOmitsEmptyIdentity(t *testing.T) {
	cl := &captureLogger{}
	Echo(cl, Fact{Kind: KindEngineState, Phase: PhaseStarted})

	if n := len(cl.records[0].args); n != 0 {
		t.Errorf("got %d args for a bare event, want 0", n)
	}
}

func TestEchoMessageIsKindOnlyWhenPhaseless(t *testing.T) {
	cl := &captureLogger{}
	Echo(cl, Fact{Kind: KindEngineState})

	if cl.records[0].msg != "EngineState" {
		t.Errorf("msg = %q, want %q", cl.records[0].msg, "EngineState")
	}
}

func TestEchoSkipsDataChange(t *testing.T) {
	cl := &captureLogger{}
	Echo(cl, Fact{Kind: KindDataChange, Phase: PhaseValueUpdated})

	if len(cl.records) != 0 {
		t.Errorf("data change echoed %d records, want 0", len(cl.records))
	}
}

func TestEchoNilLoggerIsNoOp(t *testing.T) {
	// Must not panic and must write nothing.
	Echo(nil, Fact{Kind: KindEngineState, Phase: PhaseStarted})
}

func TestNewEchoReporterNilLoggerFallsBackToDefault(t *testing.T) {
	s := NewEchoReporter(nil)
	if s == nil {
		t.Fatal("NewEchoReporter(nil) = nil")
	}

	// Backed by slog.Default(); Report must not panic.
	s.Report(Fact{Kind: KindEngineState, Phase: PhaseStarted})
}

func TestEchoSinkEmitWritesToLogger(t *testing.T) {
	cl := &captureLogger{}
	NewEchoReporter(cl).Report(Fact{Kind: KindEngineState, Phase: PhaseStarted})

	if len(cl.records) != 1 || cl.records[0].level != "INFO" {
		t.Fatalf("expected one INFO record, got %+v", cl.records)
	}
}
