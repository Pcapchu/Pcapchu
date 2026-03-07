package logger

import (
	"context"
	"log/slog"
)

// SlogSink implements Sink by delegating to a *slog.Logger.
// SpanEvent and RecordMetric are emitted as structured log messages.
type SlogSink struct {
	logger *slog.Logger
}

// NewSlogSink creates a Sink backed by the given slog.Logger.
// If logger is nil, slog.Default() is used.
func NewSlogSink(logger *slog.Logger) Sink {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogSink{logger: logger}
}

func (s *SlogSink) Log(ctx context.Context, level Level, msg string, attrs ...Attr) {
	s.logger.LogAttrs(ctx, slogLevel(level), msg, toSlogAttrs(attrs)...)
}

func (s *SlogSink) SpanEvent(ctx context.Context, name string, attrs ...Attr) {
	s.logger.LogAttrs(ctx, slog.LevelInfo, "span.event: "+name, toSlogAttrs(attrs)...)
}

func (s *SlogSink) RecordMetric(ctx context.Context, name string, value float64, attrs ...Attr) {
	all := append([]Attr{A("metric_value", value)}, attrs...)
	s.logger.LogAttrs(ctx, slog.LevelInfo, "metric: "+name, toSlogAttrs(all)...)
}

func (s *SlogSink) Flush(context.Context) {} // slog has no flush

// --- helpers ---

func slogLevel(l Level) slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func toSlogAttrs(attrs []Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = slog.Any(a.Key, a.Value)
	}
	return out
}
