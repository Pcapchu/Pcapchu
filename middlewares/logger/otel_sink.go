package logger

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// OTelSink implements Sink using OpenTelemetry logs, trace spans, and metrics.
// Log records are emitted via the OTel Logs API (no active span required).
// SpanEvent still attaches events to the current span when available, and also
// emits a log record so nothing is silently lost.
type OTelSink struct {
	tracer trace.Tracer
	meter  metric.Meter
	logger otellog.Logger

	// Lazily-created metric instruments, keyed by metric name.
	mu         sync.RWMutex
	histograms map[string]metric.Float64Histogram
	counters   map[string]metric.Float64Counter
}

// NewOTelSink creates a Sink backed by OTel logs, tracing, and metrics.
// tracer, meter, and logger should come from a configured OTelResources.
func NewOTelSink(tracer trace.Tracer, meter metric.Meter, logger otellog.Logger) Sink {
	return &OTelSink{
		tracer:     tracer,
		meter:      meter,
		logger:     logger,
		histograms: make(map[string]metric.Float64Histogram),
		counters:   make(map[string]metric.Float64Counter),
	}
}

// levelToSeverity maps our Level to OTel log Severity.
func levelToSeverity(level Level) otellog.Severity {
	switch level {
	case LevelDebug:
		return otellog.SeverityDebug1
	case LevelInfo:
		return otellog.SeverityInfo1
	case LevelWarn:
		return otellog.SeverityWarn1
	case LevelError:
		return otellog.SeverityError1
	default:
		return otellog.SeverityInfo1
	}
}

// toLogAttrs converts our Attr slice to OTel log KeyValue slice.
func toLogAttrs(attrs []Attr) []otellog.KeyValue {
	out := make([]otellog.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		switch v := a.Value.(type) {
		case string:
			out = append(out, otellog.String(a.Key, v))
		case int:
			out = append(out, otellog.Int(a.Key, v))
		case int64:
			out = append(out, otellog.Int64(a.Key, v))
		case float64:
			out = append(out, otellog.Float64(a.Key, v))
		case bool:
			out = append(out, otellog.Bool(a.Key, v))
		default:
			out = append(out, otellog.String(a.Key, fmt.Sprintf("%v", v)))
		}
	}
	return out
}

func (o *OTelSink) Log(ctx context.Context, level Level, msg string, attrs ...Attr) {
	if o.logger == nil {
		return
	}
	var rec otellog.Record
	rec.SetTimestamp(time.Now())
	rec.SetSeverity(levelToSeverity(level))
	rec.SetSeverityText(level.String())
	rec.SetBody(otellog.StringValue(msg))
	rec.AddAttributes(toLogAttrs(attrs)...)
	o.logger.Emit(ctx, rec)
}

func (o *OTelSink) SpanEvent(ctx context.Context, name string, attrs ...Attr) {
	// Attach to active span if available.
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.AddEvent(name, trace.WithAttributes(toOTelAttrs(attrs)...))
	}
	// Always emit as a log record so events are never silently lost.
	if o.logger != nil {
		var rec otellog.Record
		rec.SetTimestamp(time.Now())
		rec.SetSeverity(otellog.SeverityInfo1)
		rec.SetSeverityText("INFO")
		rec.SetBody(otellog.StringValue(name))
		rec.AddAttributes(toLogAttrs(attrs)...)
		o.logger.Emit(ctx, rec)
	}
}

func (o *OTelSink) RecordMetric(ctx context.Context, name string, value float64, attrs ...Attr) {
	if o.meter == nil {
		return
	}
	otelAttrs := toOTelAttrs(attrs)
	attrSet := metric.WithAttributes(otelAttrs...)

	// Use histogram for duration-like metrics, counter for everything else.
	if isDurationMetric(name) {
		h := o.getOrCreateHistogram(name)
		if h != nil {
			h.Record(ctx, value, attrSet)
		}
	} else {
		c := o.getOrCreateCounter(name)
		if c != nil {
			c.Add(ctx, value, attrSet)
		}
	}
}

func (o *OTelSink) Flush(ctx context.Context) {
	// TracerProvider / MeterProvider flush is handled externally via the
	// shutdown function returned by InitOTel. This is intentional — the
	// Sink shouldn't own the provider lifecycle.
}

// --- lazy instrument creation ---

func (o *OTelSink) getOrCreateHistogram(name string) metric.Float64Histogram {
	o.mu.RLock()
	h, ok := o.histograms[name]
	o.mu.RUnlock()
	if ok {
		return h
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if h, ok = o.histograms[name]; ok {
		return h
	}
	h, err := o.meter.Float64Histogram(name)
	if err != nil {
		return nil
	}
	o.histograms[name] = h
	return h
}

func (o *OTelSink) getOrCreateCounter(name string) metric.Float64Counter {
	o.mu.RLock()
	c, ok := o.counters[name]
	o.mu.RUnlock()
	if ok {
		return c
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if c, ok = o.counters[name]; ok {
		return c
	}
	c, err := o.meter.Float64Counter(name)
	if err != nil {
		return nil
	}
	o.counters[name] = c
	return c
}

// --- helpers ---

func toOTelAttrs(attrs []Attr) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		switch v := a.Value.(type) {
		case string:
			out = append(out, attribute.String(a.Key, v))
		case int:
			out = append(out, attribute.Int(a.Key, v))
		case int64:
			out = append(out, attribute.Int64(a.Key, v))
		case float64:
			out = append(out, attribute.Float64(a.Key, v))
		case bool:
			out = append(out, attribute.Bool(a.Key, v))
		default:
			out = append(out, attribute.String(a.Key, fmt.Sprintf("%v", v)))
		}
	}
	return out
}

func isDurationMetric(name string) bool {
	// Convention: metrics ending in ".duration" or ".latency" are histograms.
	return len(name) > 9 && (name[len(name)-9:] == ".duration" || name[len(name)-8:] == ".latency")
}
