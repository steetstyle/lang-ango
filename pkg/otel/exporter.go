package otel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type BufferedSpanProcessor struct {
	exporter sdktrace.SpanExporter
	pending  map[string][]sdktrace.ReadOnlySpan
	mu       sync.Mutex
}

func NewBufferedSpanProcessor(exporter sdktrace.SpanExporter) *BufferedSpanProcessor {
	return &BufferedSpanProcessor{
		exporter: exporter,
		pending:  make(map[string][]sdktrace.ReadOnlySpan),
	}
}

func (b *BufferedSpanProcessor) OnStart(parent context.Context, span sdktrace.ReadWriteSpan) {}
func (b *BufferedSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	// Add to buffer when span ends
	traceID := span.SpanContext().TraceID().String()
	b.mu.Lock()
	b.pending[traceID] = append(b.pending[traceID], span)
	b.mu.Unlock()
}

func (b *BufferedSpanProcessor) Shutdown(ctx context.Context) error {
	return b.exporter.Shutdown(ctx)
}

func (b *BufferedSpanProcessor) ForceFlush(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Export all pending spans
	for traceID, spans := range b.pending {
		if len(spans) > 0 {
			if err := b.exporter.ExportSpans(ctx, spans); err != nil {
				fmt.Printf("[BUFFER] Failed to export trace %s: %v\n", traceID, err)
			} else {
				fmt.Printf("[BUFFER] Exported trace %s with %d spans\n", traceID, len(spans))
			}
		}
		delete(b.pending, traceID)
	}
	return nil
}

type Exporter struct {
	traceProvider *sdktrace.TracerProvider
	meterProvider *sdkmetric.MeterProvider
	config        *Config
	spanProcessor *BufferedSpanProcessor
	traceClient   collectortracev1.TraceServiceClient
	grpcConn      grpc.ClientConnInterface
}

func (e *Exporter) Flush() {
	if e.spanProcessor != nil {
		e.spanProcessor.ForceFlush(context.Background())
	}
}

type Config struct {
	OTelEndpoint   string
	OTelInsecure   bool
	OTelProtocol   string
	ServiceName    string
	ServiceVersion string
	Environment    string
}

func NewExporter(cfg *Config) (*Exporter, error) {
	ctx := context.Background()

	var traceExporter *otlptrace.Exporter
	var err error

	protocol := cfg.OTelProtocol
	if protocol == "" {
		protocol = "grpc"
	}

	if protocol == "http" || protocol == "http/protobuf" {
		traceOpts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.OTelEndpoint),
		}
		if cfg.OTelInsecure {
			traceOpts = append(traceOpts, otlptracehttp.WithInsecure())
		}
		traceExporter, err = otlptracehttp.New(ctx, traceOpts...)
	} else {
		traceOpts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.OTelEndpoint),
		}
		if cfg.OTelInsecure {
			traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
		}
		traceExporter, err = otlptracegrpc.New(ctx, traceOpts...)
	}
	if err != nil {
		return nil, fmt.Errorf("creating trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	// Create buffered span processor - exports all spans in a trace together
	bsp := NewBufferedSpanProcessor(traceExporter)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(bsp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	promExporter, err := prometheus.New()
	if err != nil {
		return nil, fmt.Errorf("creating prometheus exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	var grpcConn grpc.ClientConnInterface
	var traceClient collectortracev1.TraceServiceClient

	if protocol == "grpc" || protocol == "" {
		grpcOpts := []grpc.DialOption{}
		if cfg.OTelInsecure {
			grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		}
		conn, err := grpc.Dial(cfg.OTelEndpoint, grpcOpts...)
		if err == nil {
			grpcConn = conn
			traceClient = collectortracev1.NewTraceServiceClient(conn)
		}
	}

	return &Exporter{
		traceProvider: tp,
		meterProvider: mp,
		config:        cfg,
		spanProcessor: bsp,
		traceClient:   traceClient,
		grpcConn:      grpcConn,
	}, nil
}

func (e *Exporter) Shutdown(ctx context.Context) error {
	if err := e.traceProvider.Shutdown(ctx); err != nil {
		return err
	}
	if e.meterProvider != nil {
		if err := e.meterProvider.Shutdown(ctx); err != nil {
			return err
		}
	}
	if conn, ok := e.grpcConn.(interface{ Close() error }); ok {
		conn.Close()
	}
	return nil
}

func (e *Exporter) Close() error {
	return e.Shutdown(context.Background())
}

func (e *Exporter) AddEvent(name string, attrs []attribute.KeyValue) {
	tracer := e.traceProvider.Tracer("lang-ango")
	ctx, span := tracer.Start(context.Background(), name)
	for _, attr := range attrs {
		span.SetAttributes(attr)
	}
	span.End()
	_ = ctx
}

type IPCSpanContext struct {
	TraceID  [16]byte
	SpanID   [8]byte
	ParentID [8]byte
}

type DirectSpan struct {
	IPCSpanContext
	Name      string
	StartTime time.Time
	EndTime   time.Time
	Attrs     []attribute.KeyValue
}

func (e *Exporter) ExportDirectSpans(spans []DirectSpan) error {
	return e.ExportSpansDirect(spans)
}

func (e *Exporter) ExportSpansDirect(spans []DirectSpan) error {
	if len(spans) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	traceID := spans[0].TraceID
	resourceAttrs := e.buildResourceAttrs()

	otlpSpans := make([]*tracev1.Span, 0, len(spans))
	for _, s := range spans {
		otlpSpans = append(otlpSpans, e.buildOTLPSpan(s, traceID))
	}

	req := &collectortracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{{
			Resource: &resourcev1.Resource{
				Attributes: resourceAttrs,
			},
			ScopeSpans: []*tracev1.ScopeSpans{{
				Scope: &commonv1.InstrumentationScope{
					Name: "lang-ango",
				},
				Spans: otlpSpans,
			}},
		}},
	}

	return e.exportDirect(ctx, req)
}

func (e *Exporter) buildResourceAttrs() []*commonv1.KeyValue {
	return []*commonv1.KeyValue{
		{Key: "service.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: e.config.ServiceName}}},
		{Key: "service.version", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: e.config.ServiceVersion}}},
		{Key: "deployment.environment", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: e.config.Environment}}},
	}
}

func (e *Exporter) buildOTLPSpan(s DirectSpan, traceID [16]byte) *tracev1.Span {
	span := &tracev1.Span{
		TraceId:           traceID[:],
		SpanId:            s.SpanID[:],
		TraceState:        "",
		ParentSpanId:      s.ParentID[:],
		Name:              s.Name,
		Kind:              tracev1.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: uint64(s.StartTime.UnixNano()),
		EndTimeUnixNano:   uint64(s.EndTime.UnixNano()),
		Attributes:        make([]*commonv1.KeyValue, 0, len(s.Attrs)),
	}

	for _, attr := range s.Attrs {
		span.Attributes = append(span.Attributes, attrToOTLP(attr))
	}

	return span
}

func attrToOTLP(attr attribute.KeyValue) *commonv1.KeyValue {
	kv := &commonv1.KeyValue{
		Key:   string(attr.Key),
		Value: &commonv1.AnyValue{},
	}

	switch attr.Value.Type() {
	case attribute.STRING:
		kv.Value.Value = &commonv1.AnyValue_StringValue{StringValue: attr.Value.AsString()}
	case attribute.INT64:
		kv.Value.Value = &commonv1.AnyValue_IntValue{IntValue: attr.Value.AsInt64()}
	case attribute.FLOAT64:
		kv.Value.Value = &commonv1.AnyValue_DoubleValue{DoubleValue: attr.Value.AsFloat64()}
	case attribute.BOOL:
		kv.Value.Value = &commonv1.AnyValue_BoolValue{BoolValue: attr.Value.AsBool()}
	default:
		kv.Value.Value = &commonv1.AnyValue_StringValue{StringValue: attr.Value.AsString()}
	}

	return kv
}

func (e *Exporter) exportDirect(ctx context.Context, req *collectortracev1.ExportTraceServiceRequest) error {
	if e.traceClient == nil {
		fmt.Printf("[DIRECT-OTLP] No gRPC client, skipping export\n")
		return nil
	}

	fmt.Printf("[DIRECT-OTLP] Exporting %d spans\n", len(req.ResourceSpans[0].ScopeSpans[0].Spans))
	for _, s := range req.ResourceSpans[0].ScopeSpans[0].Spans {
		fmt.Printf("[DIRECT-OTLP] Span: trace=%x, span=%x, parent=%x, name=%s\n", s.TraceId, s.SpanId, s.ParentSpanId, s.Name)
	}

	_, err := e.traceClient.Export(ctx, req)
	if err != nil {
		fmt.Printf("[DIRECT-OTLP] Export error: %v\n", err)
		return err
	}

	fmt.Printf("[DIRECT-OTLP] Export successful\n")
	return nil
}

func (e *Exporter) AddSpanWithContext(name string, ctx context.Context, spanCtx IPCSpanContext, attrs []attribute.KeyValue) context.Context {
	tracer := e.traceProvider.Tracer("lang-ango")

	// Create span context with specific IDs from IPC
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID(spanCtx.TraceID),
		SpanID:  trace.SpanID(spanCtx.SpanID),
	})

	// Check if there's a valid parent ID in the span context
	parentID := spanCtx.ParentID
	if parentID != [8]byte{} {
		nonZero := false
		for _, b := range parentID {
			if b != 0 {
				nonZero = true
				break
			}
		}
		if nonZero {
			// Create parent span context
			parentSc := trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: trace.TraceID(spanCtx.TraceID),
				SpanID:  trace.SpanID(parentID),
			})
			// Set parent in context
			ctx = trace.ContextWithSpanContext(ctx, parentSc)
		}
	}

	// Set this span's context in the context
	ctx = trace.ContextWithSpanContext(ctx, sc)

	// Start span with context that has this span's context
	ctx, span := tracer.Start(ctx, name)
	for _, attr := range attrs {
		span.SetAttributes(attr)
	}

	span.End()

	return ctx
}

func (e *Exporter) StartSpan(name string) (context.Context, trace.Span) {
	tracer := e.traceProvider.Tracer("lang-ango")
	return tracer.Start(context.Background(), name)
}

// NewExporterWithTracerProvider is for testing: it uses the given TracerProvider
// so tests can attach a recording SpanProcessor and assert on exported spans.
func NewExporterWithTracerProvider(tp *sdktrace.TracerProvider) *Exporter {
	return &Exporter{traceProvider: tp, config: &Config{}}
}
