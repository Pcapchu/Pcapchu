package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
)

// ANSI colour codes.
const (
	ansiReset = "\033[0m"

	ansiRed          = 31
	ansiGreen        = 32
	ansiYellow       = 33
	ansiMagenta      = 35
	ansiCyan         = 36
	ansiLightGray    = 37
	ansiDarkGray     = 90
	ansiLightRed     = 91
	ansiLightGreen   = 92
	ansiLightMagenta = 95
	ansiLightCyan    = 96
	ansiWhite        = 97
	ansiLightBlue    = 94
)

func ansiColorize(code int, v string) string {
	return fmt.Sprintf("\033[%sm%s%s", strconv.Itoa(code), v, ansiReset)
}

const prettyTimeFormat = "2006-01-02 15:04:05.000"

// prettyHandler is a minimal slog.Handler that produces colored, human-readable
// output. Level labels are printed without padding or centering.
type prettyHandler struct {
	out       io.Writer
	mu        *sync.Mutex
	level     slog.Level
	colorful  bool
	multiline bool
	preAttrs  []slog.Attr
	groups    []string
}

func newPrettyHandler(out io.Writer, level slog.Level, colorful, multiline bool) *prettyHandler {
	return &prettyHandler{
		out:      out,
		mu:       &sync.Mutex{},
		level:    level,
		colorful: colorful,
		multiline: multiline,
	}
}

func (h *prettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	buf := make([]byte, 0, 512)

	// Timestamp
	if !r.Time.IsZero() {
		ts := r.Time.Format(prettyTimeFormat)
		if h.colorful {
			ts = ansiColorize(ansiLightGray, ts)
		}
		buf = fmt.Appendf(buf, "%s ", ts)
	}

	// Level — no padding, just the colored label
	buf = append(buf, h.levelStr(r.Level)...)

	// Message
	msg := r.Message
	if h.colorful {
		msg = ansiColorize(ansiWhite, msg)
	}
	buf = fmt.Appendf(buf, " %s", msg)

	// Attributes
	if h.multiline {
		for _, a := range h.preAttrs {
			buf = h.appendMultilineAttr(buf, a, 1)
		}
		hasAttrs := len(h.preAttrs) > 0
		r.Attrs(func(a slog.Attr) bool {
			if !hasAttrs {
				buf = append(buf, '\n')
				hasAttrs = true
			}
			buf = h.appendMultilineAttr(buf, a, 1)
			return true
		})
	} else {
		for _, a := range h.preAttrs {
			buf = h.appendInlineAttr(buf, a)
		}
		r.Attrs(func(a slog.Attr) bool {
			buf = h.appendInlineAttr(buf, a)
			return true
		})
	}

	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.out.Write(buf)
	return err
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	h2 := h.clone()
	h2.preAttrs = append(h2.preAttrs, attrs...)
	return h2
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := h.clone()
	h2.groups = append(h2.groups, name)
	return h2
}

func (h *prettyHandler) clone() *prettyHandler {
	h2 := *h
	h2.preAttrs = make([]slog.Attr, len(h.preAttrs))
	copy(h2.preAttrs, h.preAttrs)
	h2.groups = make([]string, len(h.groups))
	copy(h2.groups, h.groups)
	return &h2
}

func (h *prettyHandler) levelStr(level slog.Level) string {
	var label string
	var code int
	switch level {
	case slog.LevelDebug:
		label, code = "DEBUG", ansiLightMagenta
	case slog.LevelInfo:
		label, code = "INFO", ansiLightCyan
	case slog.LevelWarn:
		label, code = "WARN", ansiLightRed
	case slog.LevelError:
		label, code = "ERROR", ansiRed
	default:
		label = level.String()
		code = ansiWhite
	}
	if h.colorful {
		return ansiColorize(code, label)
	}
	return label
}

func (h *prettyHandler) appendInlineAttr(buf []byte, a slog.Attr) []byte {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return buf
	}
	key := a.Key
	val := a.Value.String()
	if h.colorful {
		return fmt.Appendf(buf, " %s=%s",
			ansiColorize(ansiLightMagenta, key),
			ansiColorize(ansiLightBlue, fmt.Sprintf("%q", val)))
	}
	return fmt.Appendf(buf, " %s=%q", key, val)
}

func (h *prettyHandler) appendMultilineAttr(buf []byte, a slog.Attr, indent int) []byte {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return buf
	}

	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	key := a.Key
	val := a.Value.String()

	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		if len(attrs) == 0 {
			return buf
		}
		if h.colorful {
			buf = fmt.Appendf(buf, "%s%s:\n", prefix, ansiColorize(ansiMagenta, key))
		} else {
			buf = fmt.Appendf(buf, "%s%s:\n", prefix, key)
		}
		for _, ga := range attrs {
			buf = h.appendMultilineAttr(buf, ga, indent+1)
		}
		return buf
	}

	if h.colorful {
		valColor := ansiLightBlue
		if a.Value.Kind() == slog.KindBool {
			if a.Value.Bool() {
				valColor = ansiLightGreen
			} else {
				valColor = ansiLightRed
			}
		}
		buf = fmt.Appendf(buf, "%s%s: %s\n", prefix,
			ansiColorize(ansiLightMagenta, key),
			ansiColorize(valColor, val))
	} else {
		buf = fmt.Appendf(buf, "%s%s: %s\n", prefix, key, val)
	}
	return buf
}
