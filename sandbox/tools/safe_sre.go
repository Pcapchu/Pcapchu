package tools

import (
	"context"

	"github.com/cloudwego/eino-ext/components/tool/commandline"
	"github.com/cloudwego/eino/components/tool"
)

// NewSafeStrReplaceEditor creates a str_replace_editor tool wrapped with SafeToolWrapper
// so that invocation errors are returned as string results instead of hard failures.
func NewSafeStrReplaceEditor(ctx context.Context, op commandline.Operator) tool.InvokableTool {
	sre, err := commandline.NewStrReplaceEditor(ctx, &commandline.EditorConfig{Operator: op})
	if err != nil {
		panic("tools: create str_replace_editor: " + err.Error())
	}
	return WrapToolSafe(sre)
}
