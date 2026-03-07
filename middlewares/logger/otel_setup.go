package logger

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
)

// OTelResources holds the providers and instruments created by InitOTel.
// Use Tracer(), Meter(), and Logger() to create an OTelSink.
type OTelResources struct {
	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider
	lp *sdklog.LoggerProvider
}

// Tracer returns a named tracer from the provider.
func (r *OTelResources) Tracer(name string) trace.Tracer {
	return r.tp.Tracer(name)
}

// Meter returns a named meter from the provider.
func (r *OTelResources) Meter(name string) metric.Meter {
	return r.mp.Meter(name)
}

// Logger returns a named logger from the log provider.
func (r *OTelResources) Logger(name string) otellog.Logger {
	return r.lp.Logger(name)
}

// Shutdown flushes and shuts down all providers. Call this on application exit.
func (r *OTelResources) Shutdown(ctx context.Context) error {
	var firstErr error
	if err := r.tp.Shutdown(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := r.mp.Shutdown(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := r.lp.Shutdown(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// InitOTel bootstraps OpenTelemetry with OTLP gRPC exporters for traces, metrics, and logs.
// All connection settings are read from standard OTel environment variables:
//   - OTEL_EXPORTER_OTLP_ENDPOINT  (default "localhost:4317")
//   - OTEL_EXPORTER_OTLP_HEADERS
//   - OTEL_EXPORTER_OTLP_TIMEOUT
//   - OTEL_EXPORTER_OTLP_INSECURE  (set "true" to disable TLS)
//
// serviceName sets the service.name resource attribute (e.g. "pcapchu").
// It can be overridden by the OTEL_SERVICE_NAME env var.
func InitOTel(ctx context.Context, serviceName string) (*OTelResources, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	// --- Trace exporter (reads OTEL_EXPORTER_OTLP_* env vars) ---
	traceExporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otel trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// --- Metric exporter (reads OTEL_EXPORTER_OTLP_* env vars) ---
	metricExporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otel metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	// --- Log exporter (reads OTEL_EXPORTER_OTLP_* env vars) ---
	logExporter, err := otlploggrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otel log exporter: %w", err)
	}

	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	global.SetLoggerProvider(lp)

	return &OTelResources{tp: tp, mp: mp, lp: lp}, nil
}
