package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// OutputGuard wraps an InvokableTool and truncates its output when it exceeds
// a configurable threshold. The truncated result tells the model the original
// length and asks it to use filtering commands (head, tail, grep, etc.).
//
// Fields:
//   - MaxOutputLen:  character threshold that triggers truncation (default 80000).
//   - TruncateKeep: how many characters to keep after truncation (default 50).
type OutputGuard struct {
	inner        tool.InvokableTool
	MaxOutputLen int
	TruncateKeep int
}

const (
	DefaultMaxOutputLen  = 80_000 // generous default — only prevents explosion
	DefaultTruncateKeep  = 500     // show just enough for orientation
)

// NewOutputGuard creates an OutputGuard with default settings.
func NewOutputGuard(inner tool.InvokableTool) *OutputGuard {
	return &OutputGuard{
		inner:        inner,
		MaxOutputLen: DefaultMaxOutputLen,
		TruncateKeep: DefaultTruncateKeep,
	}
}

func (g *OutputGuard) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return g.inner.Info(ctx)
}

func (g *OutputGuard) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	result, err := g.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		return result, err
	}

	maxLen := g.MaxOutputLen
	if maxLen <= 0 {
		maxLen = DefaultMaxOutputLen
	}

	if len(result) <= maxLen {
		return result, nil
	}

	keep := g.TruncateKeep
	if keep <= 0 {
		keep = DefaultTruncateKeep
	}

	preview := result[:keep]
	return fmt.Sprintf(
		"[WARNING] Command output too large (%d characters, threshold %d). Output truncated.\n"+
			"First %d characters:\n%s\n\n"+
			"Please use filtering commands (head, tail, grep, awk, wc -l, etc.) to limit output size.",
		len(result), maxLen, keep, preview,
	), nil
}
