package otel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

type Exporter struct {
	traceProvider *sdktrace.TracerProvider
	meterProvider *sdkmetric.MeterProvider
	config        *Config
}

type Config struct {
	OTelEndpoint   string
	OTelInsecure   bool
	ServiceName    string
	ServiceVersion string
	Environment    string
}

func NewExporter(cfg *Config) (*Exporter, error) {
	ctx := context.Background()

	traceOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.OTelEndpoint),
	}
	if cfg.OTelInsecure {
		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
	}

	traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
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

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
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

	return &Exporter{
		traceProvider: tp,
		meterProvider: mp,
		config:        cfg,
	}, nil
}

func (e *Exporter) Shutdown(ctx context.Context) error {
	if err := e.traceProvider.Shutdown(ctx); err != nil {
		return err
	}
	return e.meterProvider.Shutdown(ctx)
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

func (e *Exporter) StartSpan(name string) (context.Context, trace.Span) {
	tracer := e.traceProvider.Tracer("lang-ango")
	return tracer.Start(context.Background(), name)
}
