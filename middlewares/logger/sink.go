package logger

import "context"

// Level represents log severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Attr is a structured key-value pair attached to log/event/metric records.
type Attr struct {
	Key   string
	Value any
}

// A creates an Attr from a key and value.
func A(key string, value any) Attr {
	return Attr{Key: key, Value: value}
}

// EmitFunc is a function that broadcasts a domain event. The eventType
// identifies the event (e.g. "step.started"), and data can be any struct
// that will be marshaled by the consumer. Used by Logger to bridge domain
// events without importing internal/events.
type EmitFunc func(eventType string, data any)

// Reserved attribute keys — use these for consistency across sinks.
const (
	AttrComponent       = "component"        // e.g. "ChatModel", "Tool", "ReActAgent"
	AttrNodeName        = "node_name"        // graph node name
	AttrDurationMs      = "duration_ms"      // call duration in milliseconds
	AttrStepID          = "step_id"          // executor step number
	AttrError           = "error"            // error message string
	AttrMessageCount    = "message_count"    // number of messages in input/output
	AttrContentLength   = "content_length"   // length of message content
	AttrCallbackStep    = "callback_step"    // callback event counter (ReAct round estimate)

	// Token usage — reserved for future TokenRecorder implementation
	AttrTokenPrompt     = "token_prompt"     // prompt/input token count
	AttrTokenCompletion = "token_completion" // completion/output token count
	AttrTokenTotal      = "token_total"      // total token count
	AttrModelName       = "model_name"       // LLM model identifier
)

// Sink is the core observability interface. Backends (slog, OTel, etc.) implement
// this to receive structured log events, trace span events, and metric data points.
//
// All methods must be safe for concurrent use.
// A nil Sink should never be passed; use NopSink instead.
type Sink interface {
	// Log emits a structured log record (equivalent to slog.Log / OTel log record).
	Log(ctx context.Context, level Level, msg string, attrs ...Attr)

	// SpanEvent attaches a named event to the current trace span.
	// No-op if there is no active span in ctx.
	SpanEvent(ctx context.Context, name string, attrs ...Attr)

	// RecordMetric records a numeric data point for the named metric.
	// The interpretation (counter add, histogram record) is up to the backend.
	RecordMetric(ctx context.Context, name string, value float64, attrs ...Attr)

	// Flush ensures all buffered data is exported. Called on shutdown.
	Flush(ctx context.Context)
}

// --- NopSink ---

// NopSink is a Sink that discards everything. Safe as a default.
type NopSink struct{}

func (NopSink) Log(context.Context, Level, string, ...Attr)        {}
func (NopSink) SpanEvent(context.Context, string, ...Attr)          {}
func (NopSink) RecordMetric(context.Context, string, float64, ...Attr) {}
func (NopSink) Flush(context.Context)                                {}

// --- MultiSink ---

// MultiSink fans out to multiple sinks. All calls are forwarded in order.
type MultiSink struct {
	sinks []Sink
}

// NewMultiSink creates a sink that delegates to all provided sinks.
// Nil entries are silently skipped.
func NewMultiSink(sinks ...Sink) Sink {
	var valid []Sink
	for _, s := range sinks {
		if s != nil {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		return NopSink{}
	}
	if len(valid) == 1 {
		return valid[0]
	}
	return &MultiSink{sinks: valid}
}

func (m *MultiSink) Log(ctx context.Context, level Level, msg string, attrs ...Attr) {
	for _, s := range m.sinks {
		s.Log(ctx, level, msg, attrs...)
	}
}

func (m *MultiSink) SpanEvent(ctx context.Context, name string, attrs ...Attr) {
	for _, s := range m.sinks {
		s.SpanEvent(ctx, name, attrs...)
	}
}

func (m *MultiSink) RecordMetric(ctx context.Context, name string, value float64, attrs ...Attr) {
	for _, s := range m.sinks {
		s.RecordMetric(ctx, name, value, attrs...)
	}
}

func (m *MultiSink) Flush(ctx context.Context) {
	for _, s := range m.sinks {
		s.Flush(ctx)
	}
}

// --- Future extension stubs ---
//
// TokenRecorder — planned interface for accumulating token usage across calls:
//   RecordTokens(ctx, promptTokens, completionTokens int, attrs ...Attr)
//   GetTotals() (prompt, completion int64)
//
// TraceLinker — planned interface for creating child spans / linking agent steps:
//   StartSpan(ctx, name string, attrs ...Attr) (context.Context, func())
