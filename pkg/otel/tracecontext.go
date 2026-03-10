package otel

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/propagation"
)

const (
	TraceparentHeader = "traceparent"
	TraceStateHeader  = "tracestate"
	BaggageHeader     = "baggage"

	Version          = "00"
	MaxTracestateLen = 512
)

type TraceContext struct {
	propagator propagation.TextMapPropagator
	mu         sync.RWMutex
	traceID    [16]byte
	spanID     [8]byte
}

type SpanContext struct {
	TraceID    string
	SpanID     string
	TraceFlags string
	TraceState string
}

func NewTraceContext() *TraceContext {
	tc := &TraceContext{
		propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}
	rand.Seed(1234)
	return tc
}

func (tc *TraceContext) GenerateTraceID() [16]byte {
	var tid [16]byte
	rand.Read(tid[:])
	tid[0] &= 0x7F
	return tid
}

func (tc *TraceContext) GenerateSpanID() [8]byte {
	var sid [8]byte
	rand.Read(sid[:])
	sid[0] &= 0x7F
	return sid
}

func (tc *TraceContext) ExtractTraceparent(headers map[string]string) (*SpanContext, error) {
	traceparent, ok := headers[TraceparentHeader]
	if !ok {
		return nil, fmt.Errorf("traceparent header not found")
	}

	parts := strings.Split(traceparent, "-")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid traceparent format")
	}

	version := parts[0]
	if version != Version {
		return nil, fmt.Errorf("unsupported traceparent version: %s", version)
	}

	traceID := parts[1]
	spanID := parts[2]

	traceFlags := "01"
	if len(parts) >= 4 {
		traceFlags = parts[3]
	}

	traceState := ""
	if tracestate, ok := headers[TraceStateHeader]; ok {
		traceState = tracestate
	}

	return &SpanContext{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: traceFlags,
		TraceState: traceState,
	}, nil
}

func (tc *TraceContext) GenerateTraceparent(tid [16]byte, sid [8]byte, sampled bool) string {
	traceIDHex := hex.EncodeToString(tid[:])
	spanIDHex := hex.EncodeToString(sid[:])

	flags := "00"
	if sampled {
		flags = "01"
	}

	return fmt.Sprintf("%s-%s-%s-%s", Version, traceIDHex, spanIDHex, flags)
}

func (tc *TraceContext) InjectTraceparent(tid [16]byte, sid [8]byte, sampled bool) map[string]string {
	return map[string]string{
		TraceparentHeader: tc.GenerateTraceparent(tid, sid, sampled),
	}
}

func (tc *TraceContext) StartNewSpan(name string) (context.Context, *SpanData) {
	tid := tc.GenerateTraceID()
	sid := tc.GenerateSpanID()

	spanData := &SpanData{
		Name:       name,
		TraceID:    tid,
		SpanID:     sid,
		ParentID:   [8]byte{},
		StartTime:  0,
		EndTime:    0,
		Attributes: make(map[string]string),
		Status:     "",
	}

	ctx := context.WithValue(context.Background(), "span", spanData)
	return ctx, spanData
}

func (tc *TraceContext) StartChildSpan(ctx context.Context, name string, parent *SpanData) (context.Context, *SpanData) {
	childSpanID := tc.GenerateSpanID()

	spanData := &SpanData{
		Name:       name,
		TraceID:    parent.TraceID,
		SpanID:     childSpanID,
		ParentID:   parent.SpanID,
		StartTime:  0,
		EndTime:    0,
		Attributes: make(map[string]string),
		Status:     "",
	}

	childCtx := context.WithValue(ctx, "span", spanData)
	return childCtx, spanData
}

type SpanData struct {
	Name       string
	TraceID    [16]byte
	SpanID     [8]byte
	ParentID   [8]byte
	StartTime  int64
	EndTime    int64
	Attributes map[string]string
	Status     string
}

func (sd *SpanData) SetAttribute(key, value string) {
	sd.Attributes[key] = value
}

func (sd *SpanData) SetStatus(status string) {
	sd.Status = status
}

func (sd *SpanData) GetTraceIDHex() string {
	return hex.EncodeToString(sd.TraceID[:])
}

func (sd *SpanData) GetSpanIDHex() string {
	return hex.EncodeToString(sd.SpanID[:])
}

func (tc *TraceContext) ParseW3CTraceID(traceIDStr string) ([16]byte, error) {
	var tid [16]byte

	cleaned := strings.ReplaceAll(traceIDStr, "-", "")
	if len(cleaned) != 32 {
		return tid, fmt.Errorf("invalid trace ID length: %d", len(cleaned))
	}

	decoded, err := hex.DecodeString(cleaned)
	if err != nil {
		return tid, fmt.Errorf("invalid trace ID hex: %w", err)
	}

	copy(tid[:], decoded)
	return tid, nil
}

func (tc *TraceContext) ParseW3CSpanID(spanIDStr string) ([8]byte, error) {
	var sid [8]byte

	cleaned := strings.ReplaceAll(spanIDStr, "-", "")
	if len(cleaned) != 16 {
		return sid, fmt.Errorf("invalid span ID length: %d", len(cleaned))
	}

	decoded, err := hex.DecodeString(cleaned)
	if err != nil {
		return sid, fmt.Errorf("invalid span ID hex: %w", err)
	}

	copy(sid[:], decoded)
	return sid, nil
}

func (tc *TraceContext) IsSampled(traceFlags string) bool {
	if len(traceFlags) >= 2 {
		flag := traceFlags[1]
		return (flag & 0x01) == 0x01
	}
	return false
}

type W3CPropagator struct {
	tc *TraceContext
}

func NewW3CPropagator() *W3CPropagator {
	return &W3CPropagator{
		tc: NewTraceContext(),
	}
}

func (p *W3CPropagator) Inject(ctx context.Context, carrier propagation.MapCarrier) {
	spanData := ctx.Value("span")
	if spanData == nil {
		return
	}

	sd := spanData.(*SpanData)
	carrier[TraceparentHeader] = p.tc.GenerateTraceparent(sd.TraceID, sd.SpanID, true)
}

func (p *W3CPropagator) Extract(ctx context.Context, carrier propagation.MapCarrier) context.Context {
	traceparent := carrier.Get(TraceparentHeader)
	if traceparent == "" {
		return ctx
	}

	spanCtx, err := p.tc.ExtractTraceparent(map[string]string{TraceparentHeader: traceparent})
	if err != nil {
		return ctx
	}

	traceID, _ := p.tc.ParseW3CTraceID(spanCtx.TraceID)
	spanID, _ := p.tc.ParseW3CSpanID(spanCtx.SpanID)

	spanData := &SpanData{
		TraceID:    traceID,
		SpanID:     spanID,
		Attributes: make(map[string]string),
	}

	return context.WithValue(ctx, "span", spanData)
}

func (p *W3CPropagator) Fields() []string {
	return []string{TraceparentHeader, TraceStateHeader, BaggageHeader}
}

var (
	DefaultTracer     = NewTraceContext()
	DefaultPropagator = NewW3CPropagator()
)
