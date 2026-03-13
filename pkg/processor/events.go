package processor

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/yourorg/lang-ango/pkg/otel"
)

const (
	EventUnknown      = 0
	EventHTTPRequest  = 1
	EventHTTPResponse = 2
	EventGRPCRequest  = 3
	EventGRPCResponse = 4
	EventDBQuery      = 5
	EventDBResponse   = 6
	EventTLSData      = 7
	EventException    = 8
)

const (
	ProtoUnknown  = 0
	ProtoHTTP     = 1
	ProtoHTTP2    = 2
	ProtoGRPC     = 3
	ProtoPostgres = 4
	ProtoMySQL    = 5
	ProtoRedis    = 6
	ProtoTLS      = 9
	ProtoMSSQL    = 10
)

const (
	DirOutgoing = 0
	DirIncoming = 1
)

const (
	HTTPMethodUnknown = 0
	HTTPMethodGET     = 1
	HTTPMethodPOST    = 2
	HTTPMethodPUT     = 3
	HTTPMethodDELETE  = 4
	HTTPMethodPATCH   = 5
	HTTPMethodHEAD    = 6
	HTTPMethodOPTIONS = 7
)

type HTTPEvent struct {
	PID         uint32
	TID         uint32
	TimestampNS uint64
	Direction   uint8
	Protocol    uint8
	EventType   uint8
	Method      uint8
	SAddr       uint32
	DAddr       uint32
	Sport       uint16
	DPort       uint16
	Status      uint16
	PathLen     uint16
	RequestLen  uint64
	ResponseLen uint64
	DurationNS  uint64
	Path        [256]byte
}

type DBEvent struct {
	PID         uint32
	TID         uint32
	TimestampNS uint64
	Direction   uint8
	Protocol    uint8
	EventType   uint8
	Pad         uint8
	SAddr       uint32
	DAddr       uint32
	Sport       uint16
	DPort       uint16
	Operation   uint16
	Pad2        uint16
	DurationNS  uint64
	QueryLen    uint64
	Query       [1024]byte
}

type TLSEvent struct {
	PID         uint32
	TID         uint32
	TimestampNS uint64
	Direction   uint8
	EventType   uint8
	SAddr       uint32
	DAddr       uint32
	Sport       uint16
	DPort       uint16
	DataLen     uint32
	SSLPtr      uint64
}

type TraceEvent struct {
	PID        uint32
	TID        uint32
	Timestamp  uint64
	Exception  string
	Message    string
	StackTrace string
}

type CPUProfileEvent struct {
	PID         uint32
	CPU         uint32
	TimestampNS uint64
	StackID     uint64
	StackDepth  uint8
	Addresses   [64]uint64
}

type MethodEvent struct {
	PID         uint32
	TID         uint32
	TimestampNS uint64
	EventType   uint8
	Depth       uint8
	MethodAddr  uint64
	DurationNS  uint64
	MethodName  [128]byte
	CallStack   [32]uint64
}

type Processor struct {
	exporter    *otel.Exporter
	spans       map[SpanKey]*spanContext
	traceConfig *traceConfig
}

type spanContext struct {
	traceID   trace.TraceID
	spanID    trace.SpanID
	startTime time.Time
	httpEvent *HTTPEvent
	dbEvent   *DBEvent
	span      trace.Span // HTTP server span; ended on response
}

type SpanKey struct {
	PID  uint32
	TID  uint32
	Port uint16
	Dir  uint8
}

type traceConfig struct {
	serviceName string
	environment string
	traceIDSeed uint64
}

func New(exporter *otel.Exporter, serviceName, environment string) *Processor {
	return &Processor{
		exporter: exporter,
		spans:    make(map[SpanKey]*spanContext),
		traceConfig: &traceConfig{
			serviceName: serviceName,
			environment: environment,
		},
	}
}

func (p *Processor) HandleHTTP(data []byte) error {
	if len(data) < 288 {
		return fmt.Errorf("data too short for HTTP event: %d", len(data))
	}

	var event HTTPEvent
	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &event)
	if err != nil {
		return fmt.Errorf("parsing HTTP event: %w", err)
	}

	attrs := []attribute.KeyValue{
		attribute.String("network.protocol", "http"),
		attribute.String("network.type", "ipv4"),
		attribute.Int("process.pid", int(event.PID)),
		attribute.Int("thread.id", int(event.TID)),
		attribute.String("http.method", httpMethodString(event.Method)),
		attribute.Int("server.port", int(event.DPort)),
		attribute.Int("network Peer.port", int(event.Sport)),
	}

	if event.Direction == DirOutgoing {
		attrs = append(attrs,
			attribute.String("server.address", p.formatIP(event.DAddr)),
			attribute.String("url.path", string(event.Path[:event.PathLen])),
		)
	} else {
		attrs = append(attrs,
			attribute.String("server.address", p.formatIP(event.SAddr)),
			attribute.Int("http.response.status_code", int(event.Status)),
		)
	}

	if event.EventType == EventHTTPRequest {
		key := SpanKey{PID: event.PID, TID: event.TID, Port: event.DPort, Dir: event.Direction}
		_, span := p.exporter.StartSpan("http")
		for _, a := range attrs {
			span.SetAttributes(a)
		}
		p.spans[key] = &spanContext{
			traceID:   p.generateTraceID(),
			spanID:    span.SpanContext().SpanID(),
			startTime: time.Now(),
			httpEvent: &event,
			span:      span,
		}
		return nil
	}
	if event.EventType == EventHTTPResponse {
		// Match response to request: server request is Incoming (to port), response is Outgoing (from port).
		serverPort := event.Sport
		key := SpanKey{PID: event.PID, TID: event.TID, Port: serverPort, Dir: DirIncoming}
		if _, ok := p.spans[key]; !ok {
			key = SpanKey{PID: event.PID, TID: event.TID, Port: serverPort, Dir: event.Direction}
		}
		if ctx, ok := p.spans[key]; ok {
			duration := time.Duration(event.TimestampNS - ctx.httpEvent.TimestampNS)
			// Only set response-specific attributes so we don't overwrite request attrs (e.g. http.method).
			ctx.span.SetAttributes(
				attribute.Int64("duration.ns", int64(duration)),
				attribute.String("server.address", p.formatIP(event.SAddr)),
				attribute.Int("http.response.status_code", int(event.Status)),
			)
			ctx.span.End()
			delete(p.spans, key)
		}
		return nil
	}

	// Fallback: emit a single event span if we see only one side (e.g. outgoing only)
	p.exporter.AddEvent("http", attrs)
	return nil
}

func (p *Processor) HandleDB(data []byte) error {
	if len(data) < 1056 {
		return fmt.Errorf("data too short for DB event: %d", len(data))
	}

	var event DBEvent
	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &event)
	if err != nil {
		return fmt.Errorf("parsing DB event: %w", err)
	}

	protoName := "unknown"
	switch event.Protocol {
	case ProtoPostgres:
		protoName = "postgresql"
	case ProtoMySQL:
		protoName = "mysql"
	case ProtoMSSQL:
		protoName = "mssql"
	}

	attrs := []attribute.KeyValue{
		attribute.String("db.system", protoName),
		attribute.Int("process.pid", int(event.PID)),
		attribute.Int("thread.id", int(event.TID)),
		attribute.String("server.address", p.formatIP(event.DAddr)),
		attribute.Int("server.port", int(event.DPort)),
		attribute.Int64("duration.ns", int64(event.DurationNS)),
	}

	if event.Direction == DirOutgoing && event.EventType == EventDBQuery {
		query := string(event.Query[:event.QueryLen])
		attrs = append(attrs, attribute.String("db.statement", query))
	}

	if event.Direction == DirIncoming && event.EventType == EventDBResponse {
		key := SpanKey{PID: event.PID, TID: event.TID, Port: event.Sport, Dir: event.Direction}
		delete(p.spans, key)
	}

	p.exporter.AddEvent("db", attrs)
	return nil
}

func (p *Processor) HandleTLS(data []byte) error {
	if len(data) < 32 {
		return fmt.Errorf("data too short for TLS event: %d", len(data))
	}

	var event TLSEvent
	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &event)
	if err != nil {
		return fmt.Errorf("parsing TLS event: %w", err)
	}

	attrs := []attribute.KeyValue{
		attribute.String("network.protocol", "tls"),
		attribute.Int("process.pid", int(event.PID)),
		attribute.Int("thread.id", int(event.TID)),
		attribute.String("server.address", p.formatIP(event.DAddr)),
		attribute.Int("server.port", int(event.DPort)),
		attribute.Int("tls.data_len", int(event.DataLen)),
	}

	direction := "encrypt"
	if event.Direction == DirIncoming {
		direction = "decrypt"
	}
	attrs = append(attrs, attribute.String("tls.direction", direction))

	p.exporter.AddEvent("tls", attrs)
	return nil
}

func (p *Processor) HandleException(data []byte) error {
	if len(data) < 24 {
		return fmt.Errorf("data too short for exception event: %d", len(data))
	}

	var event TraceEvent
	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &event)
	if err != nil {
		return fmt.Errorf("parsing exception event: %w", err)
	}

	attrs := []attribute.KeyValue{
		attribute.String("exception.type", event.Exception),
		attribute.String("exception.message", event.Message),
		attribute.Int("process.pid", int(event.PID)),
		attribute.Int("thread.id", int(event.TID)),
	}

	if len(event.StackTrace) > 0 {
		attrs = append(attrs, attribute.String("exception.stacktrace", event.StackTrace))
	}

	p.exporter.AddEvent("exception", attrs)
	return nil
}

func (p *Processor) generateTraceID() trace.TraceID {
	return trace.TraceID{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
}

func (p *Processor) generateSpanID() trace.SpanID {
	return trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
}

func (p *Processor) formatIP(ip uint32) string {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, ip)
	return net.IP(b).String()
}

func httpMethodString(method uint8) string {
	switch method {
	case HTTPMethodGET:
		return "GET"
	case HTTPMethodPOST:
		return "POST"
	case HTTPMethodPUT:
		return "PUT"
	case HTTPMethodDELETE:
		return "DELETE"
	case HTTPMethodPATCH:
		return "PATCH"
	case HTTPMethodHEAD:
		return "HEAD"
	case HTTPMethodOPTIONS:
		return "OPTIONS"
	default:
		return "UNKNOWN"
	}
}

func (p *Processor) HandleCPUProfile(data []byte) error {
	if len(data) < 8 {
		return fmt.Errorf("data too short for CPU profile event: %d", len(data))
	}

	var event CPUProfileEvent
	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &event)
	if err != nil {
		return fmt.Errorf("parsing CPU profile event: %w", err)
	}

	attrs := []attribute.KeyValue{
		attribute.String("profiler.type", "cpu"),
		attribute.Int("process.pid", int(event.PID)),
		attribute.Int("cpu.id", int(event.CPU)),
		attribute.Int("stack.depth", int(event.StackDepth)),
	}

	if event.StackDepth > 0 {
		attrs = append(attrs, attribute.Int("stack.sample.count", 1))
	}

	p.exporter.AddEvent("cpu_profile", attrs)
	return nil
}

func (p *Processor) HandleMethodEvent(data []byte) error {
	if len(data) < 168 {
		return fmt.Errorf("data too short for method event: %d", len(data))
	}

	var event MethodEvent
	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &event)
	if err != nil {
		return fmt.Errorf("parsing method event: %w", err)
	}

	eventTypeName := "unknown"
	switch event.EventType {
	case 1:
		eventTypeName = "method_entry"
	case 2:
		eventTypeName = "method_exit"
	case 3:
		eventTypeName = "exception"
	case 4:
		eventTypeName = "thread_created"
	case 5:
		eventTypeName = "thread_destroyed"
	case 6:
		eventTypeName = "gc"
	}

	attrs := []attribute.KeyValue{
		attribute.String("dotnet.event", eventTypeName),
		attribute.Int("process.pid", int(event.PID)),
		attribute.Int("thread.id", int(event.TID)),
		attribute.Int("method.depth", int(event.Depth)),
		attribute.Int64("duration.ns", int64(event.DurationNS)),
	}

	if event.EventType == 2 && event.DurationNS > 0 {
		attrs = append(attrs, attribute.Int64("method.duration.ns", int64(event.DurationNS)))
	}

	p.exporter.AddEvent("method", attrs)
	return nil
}

func (p *Processor) GenerateTestHTTP() error {
	attrs := []attribute.KeyValue{
		attribute.String("network.protocol", "http"),
		attribute.String("network.type", "ipv4"),
		attribute.Int("process.pid", 1234),
		attribute.Int("thread.id", 5678),
		attribute.String("http.method", "GET"),
		attribute.Int("server.port", 8080),
		attribute.Int("network Peer.port", 12345),
		attribute.String("server.address", "127.0.0.1"),
		attribute.Int("http.response.status_code", 200),
		attribute.String("url.path", "/api/auto/health"),
	}
	p.exporter.AddEvent("http", attrs)
	return nil
}

func (p *Processor) GenerateTestDB() error {
	attrs := []attribute.KeyValue{
		attribute.String("db.system", "postgresql"),
		attribute.Int("process.pid", 1234),
		attribute.Int("thread.id", 5678),
		attribute.String("server.address", "127.0.0.1"),
		attribute.Int("server.port", 5432),
		attribute.Int64("duration.ns", 1000000),
		attribute.String("db.statement", "SELECT 1"),
	}
	p.exporter.AddEvent("db", attrs)
	return nil
}

func (p *Processor) GenerateTestException() error {
	attrs := []attribute.KeyValue{
		attribute.String("dotnet.event", "exception"),
		attribute.Int("process.pid", 1234),
		attribute.Int("thread.id", 5678),
		attribute.Int("method.depth", 2),
		attribute.Int64("duration.ns", 50000),
		attribute.String("exception.type", "Npgsql.PostgresException"),
		attribute.String("exception.message", "relation \"q.token\" does not exist"),
	}
	p.exporter.AddEvent("method", attrs)
	return nil
}

func (p *Processor) GenerateTestMethod() error {
	attrs := []attribute.KeyValue{
		attribute.String("dotnet.event", "method_entry"),
		attribute.Int("process.pid", 1234),
		attribute.Int("thread.id", 5678),
		attribute.Int("method.depth", 2),
		attribute.Int64("duration.ns", 50000),
	}
	p.exporter.AddEvent("method", attrs)
	return nil
}

func (p *Processor) GenerateTestCPUProfile() error {
	attrs := []attribute.KeyValue{
		attribute.String("profiler.type", "cpu"),
		attribute.Int("process.pid", 1234),
		attribute.Int("cpu.id", 0),
		attribute.Int("stack.depth", 5),
		attribute.Int("stack.sample.count", 1),
	}
	p.exporter.AddEvent("cpu_profile", attrs)
	return nil
}
