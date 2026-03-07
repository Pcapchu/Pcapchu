package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// SafeToolWrapper wraps any InvokableTool so that errors from InvokableRun are
// returned as string results instead of Go errors. This prevents the eino react
// agent from treating recoverable tool failures (e.g., invalid view_range) as
// fatal errors that kill the entire agent loop.
type SafeToolWrapper struct {
	inner tool.InvokableTool
}

// WrapToolSafe wraps a tool so its invocation errors become string results.
func WrapToolSafe(t tool.InvokableTool) tool.InvokableTool {
	return &SafeToolWrapper{inner: t}
}

func (w *SafeToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *SafeToolWrapper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	result, err := w.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		// Return the error as a tool result so the LLM can see it and retry.
		return fmt.Sprintf("[Tool Error] %s", err.Error()), nil
	}
	return result, nil
}
