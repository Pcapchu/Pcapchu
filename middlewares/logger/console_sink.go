package logger

import (
"context"
"log/slog"
"os"
)

// ConsoleSink is a Sink that prints colored, human-readable output to stdout
// via the prettyHandler. String attributes longer than maxContentLen are
// truncated for readability (0 = no truncation).
type ConsoleSink struct {
	logger        *slog.Logger
	maxContentLen int
}

const defaultMaxContentLen = 2000

// ConsoleOption configures a ConsoleSink.
type ConsoleOption func(*ConsoleSink)

// WithTruncate sets the maximum length for string attributes.
// 0 disables truncation. Default is 2000.
func WithTruncate(maxLen int) ConsoleOption {
	return func(c *ConsoleSink) { c.maxContentLen = maxLen }
}

// NewConsoleSink creates a compact single-line console sink.
func NewConsoleSink(opts ...ConsoleOption) Sink {
	h := newPrettyHandler(os.Stdout, slog.LevelDebug, true, false)
	c := &ConsoleSink{logger: slog.New(h), maxContentLen: defaultMaxContentLen}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewPrettyConsoleSink creates a multi-line aligned console sink.
func NewPrettyConsoleSink(opts ...ConsoleOption) Sink {
	h := newPrettyHandler(os.Stdout, slog.LevelDebug, true, true)
	c := &ConsoleSink{logger: slog.New(h), maxContentLen: defaultMaxContentLen}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *ConsoleSink) Log(ctx context.Context, level Level, msg string, attrs ...Attr) {
	c.logger.LogAttrs(ctx, slogLevel(level), msg, toSlogAttrs(c.truncate(attrs))...)
}

func (c *ConsoleSink) SpanEvent(ctx context.Context, name string, attrs ...Attr) {
	c.logger.LogAttrs(ctx, slog.LevelInfo, "span.event: "+name, toSlogAttrs(c.truncate(attrs))...)
}

func (c *ConsoleSink) RecordMetric(ctx context.Context, name string, value float64, attrs ...Attr) {
	all := append([]Attr{A("metric_value", value)}, attrs...)
	c.logger.LogAttrs(ctx, slog.LevelInfo, "metric: "+name, toSlogAttrs(c.truncate(all))...)
}

// truncate applies maxContentLen truncation to string attributes.
func (c *ConsoleSink) truncate(attrs []Attr) []Attr {
	return truncateAttrs(attrs, c.maxContentLen)
}

func (c *ConsoleSink) Flush(context.Context) {}
