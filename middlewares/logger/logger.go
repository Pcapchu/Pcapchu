/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package logger

import (
	"context"
	"fmt"
	"os"
	"reflect"
)

// ANSI colour escapes used by Tokenf.
const (
	colorBrown = "\033[31;1m"
	colorReset = "\033[0m"
)

// ---------------------------------------------------------------------------
// Logger — unified observability handle
// ---------------------------------------------------------------------------

// Log is the interface that consumers depend on. Pass this — not *Logger —
// across package boundaries.
type Log interface {
	Debug(ctx context.Context, msg string, attrs ...Attr)
	Info(ctx context.Context, msg string, attrs ...Attr)
	Warn(ctx context.Context, msg string, attrs ...Attr)
	Error(ctx context.Context, msg string, attrs ...Attr)
	Emit(eventType string, data any)
}

// Logger combines structured logging (via Sink) and domain event emission
// (via EmitFunc). All output is handled by the Sink chain.
// Use the chain methods to compose the desired configuration.
type Logger struct {
	sink          Sink
	emit          EmitFunc
	maxContentLen int // 0 = no truncation
}

// NewLogger creates a bare Logger with no sinks or emitters.
// Chain WithSink, WithEmit, WithMaxContentLen to configure.
func NewLogger() *Logger {
	return &Logger{}
}

// WithSink appends a Sink. Multiple calls compose via MultiSink.
func (l *Logger) WithSink(s Sink) *Logger {
	if s == nil {
		return l
	}
	if l.sink == nil {
		l.sink = s
	} else {
		l.sink = NewMultiSink(l.sink, s)
	}
	return l
}

// WithEmit sets the EmitFunc for domain event broadcasting (e.g. to SSE/streaming).
func (l *Logger) WithEmit(fn EmitFunc) *Logger {
	l.emit = fn
	return l
}

// WithMaxContentLen sets the truncation length for string Attrs sent to the
// Sink via Emit(). 0 (default) means no truncation.
func (l *Logger) WithMaxContentLen(n int) *Logger {
	l.maxContentLen = n
	return l
}

// Sink returns the underlying Sink (may be nil if no sinks were added).
func (l *Logger) Sink() Sink {
	if l.sink == nil {
		return NopSink{}
	}
	return l.sink
}

// --- Structured logging methods ---

func (l *Logger) Debug(ctx context.Context, msg string, attrs ...Attr) {
	l.Sink().Log(ctx, LevelDebug, msg, attrs...)
}

func (l *Logger) Info(ctx context.Context, msg string, attrs ...Attr) {
	l.Sink().Log(ctx, LevelInfo, msg, attrs...)
}

func (l *Logger) Warn(ctx context.Context, msg string, attrs ...Attr) {
	l.Sink().Log(ctx, LevelWarn, msg, attrs...)
}

func (l *Logger) Error(ctx context.Context, msg string, attrs ...Attr) {
	l.Sink().Log(ctx, LevelError, msg, attrs...)
}

// --- Domain events ---

// Emit processes a domain event through two paths:
//  1. Sink.SpanEvent — full untruncated attrs (each sink decides its own truncation).
//  2. EmitFunc — original untruncated data for streaming API consumers.
func (l *Logger) Emit(eventType string, data any) {
	attrs := flattenToAttrs(data)
	l.Sink().SpanEvent(context.Background(), eventType, attrs...)
	if l.emit != nil {
		l.emit(eventType, data)
	}
}

// --- Sink delegation ---

func (l *Logger) SpanEvent(ctx context.Context, name string, attrs ...Attr) {
	l.Sink().SpanEvent(ctx, name, attrs...)
}

func (l *Logger) RecordMetric(ctx context.Context, name string, value float64, attrs ...Attr) {
	l.Sink().RecordMetric(ctx, name, value, attrs...)
}

func (l *Logger) Flush(ctx context.Context) {
	l.Sink().Flush(ctx)
}

// ---------------------------------------------------------------------------
// Package-level default Logger (backward compatibility)
// ---------------------------------------------------------------------------

var defaultLogger Log = NewLogger()

// SetDefault sets the package-level default Logger used by Infof, Errorf, etc.
func SetDefault(l Log) {
	if l != nil {
		defaultLogger = l
	}
}

// Infof logs at INFO level via the default Logger.
func Infof(format string, args ...any) {
	defaultLogger.Info(context.Background(), fmt.Sprintf(format, args...))
}

// Errorf logs at ERROR level via the default Logger.
func Errorf(format string, args ...any) {
	defaultLogger.Error(context.Background(), fmt.Sprintf(format, args...))
}

// Warnf logs at WARN level via the default Logger.
func Warnf(format string, args ...any) {
	defaultLogger.Warn(context.Background(), fmt.Sprintf(format, args...))
}

// Tokenf prints token-related output directly to terminal (no structured logging).
func Tokenf(format string, args ...any) {
	fmt.Printf("%s%s%s", colorBrown, fmt.Sprintf(format, args...), colorReset)
}

// Fatalf logs at ERROR level and exits the process.
func Fatalf(format string, args ...any) {
	defaultLogger.Error(context.Background(), fmt.Sprintf(format, args...))
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// Struct → Attr flattening & truncation
// ---------------------------------------------------------------------------

// flattenToAttrs converts a struct (or pointer to struct) into a flat []Attr
// using the struct's exported fields. Field names are taken from the json tag
// (if present), otherwise lowercased. Nested structs/slices are stored as-is.
func flattenToAttrs(v any) []Attr {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return []Attr{{Key: "data", Value: v}}
	}
	rt := rv.Type()
	attrs := make([]Attr, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}
		key := field.Tag.Get("json")
		if idx := len(key); idx > 0 {
			// strip ",omitempty" etc.
			if ci := indexOf(key, ','); ci >= 0 {
				key = key[:ci]
			}
		}
		if key == "" || key == "-" {
			key = field.Name
		}
		attrs = append(attrs, Attr{Key: key, Value: rv.Field(i).Interface()})
	}
	return attrs
}

// indexOf returns the index of the first occurrence of sep in s, or -1.
func indexOf(s string, sep byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return i
		}
	}
	return -1
}

// truncateAttrs returns a copy of attrs where string values longer than
// maxLen are truncated with a "...[truncated]" suffix.
// If maxLen <= 0 the original slice is returned unmodified.
func truncateAttrs(attrs []Attr, maxLen int) []Attr {
	if maxLen <= 0 {
		return attrs
	}
	out := make([]Attr, len(attrs))
	for i, a := range attrs {
		if s, ok := a.Value.(string); ok && len(s) > maxLen {
			out[i] = Attr{Key: a.Key, Value: s[:maxLen] + "...[truncated]"}
		} else {
			out[i] = a
		}
	}
	return out
}
