package logger

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorBrown = "\033[31;1m"
	colorReset = "\033[0m"
)

// ConsoleSink is a Sink that prints colored, human-readable output to stdout.
// Use NewConsoleSink for single-line compact output, or NewPrettyConsoleSink
// for multi-line aligned output. Either can replace SlogSink when you want
// prettier terminal output.
type ConsoleSink struct {
	pretty bool
}

// NewConsoleSink creates a compact single-line console sink:
//
//	[INFO] 2006-01-02 15:04:05 msg key=val key=val
func NewConsoleSink() Sink { return &ConsoleSink{} }

// NewPrettyConsoleSink creates a multi-line aligned console sink:
//
//	[INFO] 2006-01-02 15:04:05 msg
//	    key1   : val1
//	    key2   : val2
func NewPrettyConsoleSink() Sink { return &ConsoleSink{pretty: true} }

func (c *ConsoleSink) Log(ctx context.Context, level Level, msg string, attrs ...Attr) {
	c.print(level, msg, attrs...)
}

func (c *ConsoleSink) SpanEvent(ctx context.Context, name string, attrs ...Attr) {
	c.print(LevelInfo, "span.event: "+name, attrs...)
}

func (c *ConsoleSink) RecordMetric(ctx context.Context, name string, value float64, attrs ...Attr) {
	all := append([]Attr{A("metric_value", value)}, attrs...)
	c.print(LevelInfo, "metric: "+name, all...)
}

func (c *ConsoleSink) Flush(context.Context) {}

// --- internal formatting ---

func consoleColor(level Level) (color, label string) {
	switch level {
	case LevelDebug:
		return colorGreen, "DEBUG"
	case LevelInfo:
		return colorGreen, "INFO"
	case LevelWarn:
		return colorBrown, "WARN"
	case LevelError:
		return colorRed, "ERROR"
	default:
		return colorReset, "???"
	}
}

func (c *ConsoleSink) print(level Level, msg string, attrs ...Attr) {
	if c.pretty {
		prettyPrint(level, msg, attrs...)
	} else {
		compactPrint(level, msg, attrs...)
	}
}

func compactPrint(level Level, msg string, attrs ...Attr) {
	color, label := consoleColor(level)
	ts := time.Now().Format("2006-01-02 15:04:05")
	if len(attrs) == 0 {
		fmt.Printf("%s[%s] %s %s%s\n", color, label, ts, msg, colorReset)
		return
	}
	fmt.Printf("%s[%s] %s %s", color, label, ts, msg)
	for _, a := range attrs {
		fmt.Printf(" %s=%v", a.Key, a.Value)
	}
	fmt.Printf("%s\n", colorReset)
}

func prettyPrint(level Level, msg string, attrs ...Attr) {
	color, label := consoleColor(level)
	ts := time.Now().Format("2006-01-02 15:04:05")
	if len(attrs) == 0 {
		fmt.Printf("%s[%s] %s %s%s\n", color, label, ts, msg, colorReset)
		return
	}
	fmt.Printf("%s[%s] %s %s\n", color, label, ts, msg)
	maxKey := 0
	for _, a := range attrs {
		if len(a.Key) > maxKey {
			maxKey = len(a.Key)
		}
	}
	for _, a := range attrs {
		val := fmt.Sprintf("%v", a.Value)
		pad := strings.Repeat(" ", maxKey-len(a.Key))
		if strings.ContainsRune(val, '\n') {
			indent := "    " + strings.Repeat(" ", maxKey) + "  "
			val = strings.ReplaceAll(val, "\n", "\n"+indent)
		}
		fmt.Printf("    %s%s : %s\n", a.Key, pad, val)
	}
	fmt.Print(colorReset)
}
