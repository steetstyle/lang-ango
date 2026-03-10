package processor_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"strconv"
	"testing"

	"github.com/yourorg/lang-ango/pkg/otel"
	"github.com/yourorg/lang-ango/pkg/processor"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// recordingSpanProcessor records finished spans for tests.
type recordingSpanProcessor struct {
	spans []recordedSpan
}

type recordedSpan struct {
	Name       string
	Attributes map[string]string
}

func (r *recordingSpanProcessor) OnStart(_ context.Context, _ sdktrace.ReadWriteSpan) {}
func (r *recordingSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	attrs := make(map[string]string)
	for _, kv := range s.Attributes() {
		switch kv.Value.Type() {
		case attribute.STRING:
			attrs[string(kv.Key)] = kv.Value.AsString()
		case attribute.INT64:
			attrs[string(kv.Key)] = strconv.FormatInt(kv.Value.AsInt64(), 10)
		default:
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
	}
	r.spans = append(r.spans, recordedSpan{Name: s.Name(), Attributes: attrs})
}
func (r *recordingSpanProcessor) Shutdown(_ context.Context) error { return nil }
func (r *recordingSpanProcessor) ForceFlush(_ context.Context) error { return nil }

func (r *recordingSpanProcessor) getSpans() []recordedSpan { return r.spans }

// buildHTTPRequestEvent returns binary payload for a server-side HTTP request (GET /api/auto/health).
func buildHTTPRequestEvent() []byte {
	ev := processor.HTTPEvent{
		PID:        1000,
		TID:        2000,
		TimestampNS: 1,
		Direction:  processor.DirIncoming, // server sees request as incoming
		Protocol:   processor.ProtoHTTP,
		EventType:  processor.EventHTTPRequest,
		Method:     processor.HTTPMethodGET,
		DAddr:      0x7f000001, // 127.0.0.1
		DPort:      8080,
		Sport:      54321,
		PathLen:    18,
	}
	copy(ev.Path[:], "/api/auto/health")
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, &ev)
	return buf.Bytes()
}

// buildHTTPResponseEvent returns binary payload for the matching HTTP response (200).
func buildHTTPResponseEvent() []byte {
	ev := processor.HTTPEvent{
		PID:        1000,
		TID:        2000,
		TimestampNS: 2e9,
		Direction:  processor.DirOutgoing, // server sends response
		Protocol:   processor.ProtoHTTP,
		EventType:  processor.EventHTTPResponse,
		SAddr:      0x7f000001,
		Sport:      8080,
		DPort:      54321,
		Status:     200,
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, &ev)
	return buf.Bytes()
}

// buildDBEvent returns binary payload for a PostgreSQL "SELECT 1" query.
func buildDBEvent() []byte {
	ev := processor.DBEvent{
		PID:        1000,
		TID:        2000,
		TimestampNS: 1,
		Direction:  processor.DirOutgoing,
		Protocol:   processor.ProtoPostgres,
		EventType:  processor.EventDBQuery,
		DAddr:      0x7f000001,
		DPort:      5432,
		DurationNS: 1000000,
		QueryLen:   8,
	}
	copy(ev.Query[:], "SELECT 1")
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, &ev)
	return buf.Bytes()
}

func TestProcessorEmitsHTTPSpan(t *testing.T) {
	rec := &recordingSpanProcessor{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	exp := otel.NewExporterWithTracerProvider(tp)
	proc := processor.New(exp, "lang-ango", "test")

	req := buildHTTPRequestEvent()
	resp := buildHTTPResponseEvent()

	if err := proc.HandleHTTP(req); err != nil {
		t.Fatalf("HandleHTTP(request): %v", err)
	}
	if err := proc.HandleHTTP(resp); err != nil {
		t.Fatalf("HandleHTTP(response): %v", err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := rec.getSpans()
	var httpSpan *recordedSpan
	for i := range spans {
		if spans[i].Name == "http" {
			httpSpan = &spans[i]
			break
		}
	}
	if httpSpan == nil {
		t.Fatalf("no http span found; got %d spans: %v", len(spans), spans)
	}
	if v := httpSpan.Attributes["network.protocol"]; v != "http" {
		t.Errorf("network.protocol: got %q", v)
	}
	if v := httpSpan.Attributes["http.method"]; v != "GET" {
		t.Errorf("http.method: got %q", v)
	}
	if v := httpSpan.Attributes["http.response.status_code"]; v != "200" {
		t.Errorf("http.response.status_code: got %q", v)
	}
}

func TestProcessorEmitsDBSpan(t *testing.T) {
	rec := &recordingSpanProcessor{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	exp := otel.NewExporterWithTracerProvider(tp)
	proc := processor.New(exp, "lang-ango", "test")

	dbPayload := buildDBEvent()
	if err := proc.HandleDB(dbPayload); err != nil {
		t.Fatalf("HandleDB: %v", err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := rec.getSpans()
	var dbSpan *recordedSpan
	for i := range spans {
		if spans[i].Name == "db" {
			dbSpan = &spans[i]
			break
		}
	}
	if dbSpan == nil {
		t.Fatalf("no db span found; got %d spans: %v", len(spans), spans)
	}
	if v := dbSpan.Attributes["db.system"]; v != "postgresql" {
		t.Errorf("db.system: got %q", v)
	}
	if v := dbSpan.Attributes["db.statement"]; v != "SELECT 1" {
		t.Errorf("db.statement: got %q", v)
	}
}

func TestProcessorRequestAndDBSpans(t *testing.T) {
	rec := &recordingSpanProcessor{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	exp := otel.NewExporterWithTracerProvider(tp)
	proc := processor.New(exp, "lang-ango", "test")
	// One full HTTP round-trip + one DB event
	if err := proc.HandleHTTP(buildHTTPRequestEvent()); err != nil {
		t.Fatal(err)
	}
	if err := proc.HandleDB(buildDBEvent()); err != nil {
		t.Fatal(err)
	}
	if err := proc.HandleHTTP(buildHTTPResponseEvent()); err != nil {
		t.Fatal(err)
	}
	_ = tp.ForceFlush(context.Background())

	hasHTTP := false
	hasDB := false
	for _, s := range rec.getSpans() {
		if s.Name == "http" {
			hasHTTP = true
			if s.Attributes["http.method"] != "GET" || s.Attributes["network.protocol"] != "http" {
				t.Errorf("http span bad attrs: %v", s.Attributes)
			}
		}
		if s.Name == "db" {
			hasDB = true
			if s.Attributes["db.system"] != "postgresql" || s.Attributes["db.statement"] != "SELECT 1" {
				t.Errorf("db span bad attrs: %v", s.Attributes)
			}
		}
	}
	if !hasHTTP {
		t.Error("expected http span")
	}
	if !hasDB {
		t.Error("expected db span")
	}
}

// buildMethodExitEvent returns binary payload for a .NET method exit event.
func buildMethodExitEvent() []byte {
	ev := processor.MethodEvent{
		PID:         1000,
		TID:         2000,
		TimestampNS: 1,
		EventType:   2, // method_exit
		Depth:       1,
		DurationNS:  5_000_000,
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, &ev)
	return buf.Bytes()
}

// buildCPUProfileEventPayload returns binary payload for a CPUProfileEvent sample.
func buildCPUProfileEventPayload() []byte {
	ev := processor.CPUProfileEvent{
		PID:         1000,
		CPU:         1,
		TimestampNS: 1,
		StackDepth:  4,
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, &ev)
	return buf.Bytes()
}

func TestProcessorEmitsMethodSpan(t *testing.T) {
	rec := &recordingSpanProcessor{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	exp := otel.NewExporterWithTracerProvider(tp)
	proc := processor.New(exp, "lang-ango", "test")

	payload := buildMethodExitEvent()
	if err := proc.HandleMethodEvent(payload); err != nil {
		t.Fatalf("HandleMethodEvent: %v", err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := rec.getSpans()
	var methodSpan *recordedSpan
	for i := range spans {
		if spans[i].Name == "method" {
			methodSpan = &spans[i]
			break
		}
	}
	if methodSpan == nil {
		t.Fatalf("no method span found; got %d spans: %v", len(spans), spans)
	}
	if v := methodSpan.Attributes["dotnet.event"]; v != "method_exit" {
		t.Errorf("dotnet.event: got %q, want %q", v, "method_exit")
	}
	if v := methodSpan.Attributes["method.depth"]; v != "1" {
		t.Errorf("method.depth: got %q, want %q", v, "1")
	}
	if v := methodSpan.Attributes["duration.ns"]; v == "" {
		t.Errorf("duration.ns: expected non-empty attribute")
	}
}

func TestProcessorEmitsCPUProfileSpan(t *testing.T) {
	rec := &recordingSpanProcessor{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	exp := otel.NewExporterWithTracerProvider(tp)
	proc := processor.New(exp, "lang-ango", "test")

	payload := buildCPUProfileEventPayload()
	if err := proc.HandleCPUProfile(payload); err != nil {
		t.Fatalf("HandleCPUProfile: %v", err)
	}
	_ = tp.ForceFlush(context.Background())

	spans := rec.getSpans()
	var cpuSpan *recordedSpan
	for i := range spans {
		if spans[i].Name == "cpu_profile" {
			cpuSpan = &spans[i]
			break
		}
	}
	if cpuSpan == nil {
		t.Fatalf("no cpu_profile span found; got %d spans: %v", len(spans), spans)
	}
	if v := cpuSpan.Attributes["profiler.type"]; v != "cpu" {
		t.Errorf("profiler.type: got %q, want %q", v, "cpu")
	}
	if v := cpuSpan.Attributes["cpu.id"]; v != "1" {
		t.Errorf("cpu.id: got %q, want %q", v, "1")
	}
	if v := cpuSpan.Attributes["stack.depth"]; v != "4" {
		t.Errorf("stack.depth: got %q, want %q", v, "4")
	}
}
