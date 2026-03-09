package logger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/Pcapchu/Pcapchu/middlewares/token_counter"
)

type LoggerCallback struct {
	log  Log
	Step int // callback event counter, incremented each OnStart; useful for estimating ReAct rounds
}

// NewLoggerCallback creates a LoggerCallback wired to the given Log.
func NewLoggerCallback(log Log) *LoggerCallback {
	return &LoggerCallback{log: log}
}

func (cb *LoggerCallback) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	cb.Step++
	if cb.log == nil {
		return ctx
	}

	attrs := []Attr{
		A(AttrComponent, info.Type),
		A(AttrNodeName, info.Name),
		A(AttrCallbackStep, cb.Step),
	}

	// Try typed model input.
	if mi := model.ConvCallbackInput(input); mi != nil {
		attrs = append(attrs, A(AttrMessageCount, len(mi.Messages)))
		if len(mi.Messages) > 0 {
			last := mi.Messages[len(mi.Messages)-1]
			attrs = append(attrs, A("last_role", last.Role))
			attrs = append(attrs, A("last_content_length", len(last.Content)))
			attrs = append(attrs, A("last_content", truncate(last.Content, 500)))
		}
		if mi.Config != nil && mi.Config.Model != "" {
			attrs = append(attrs, A(AttrModelName, mi.Config.Model))
		}
		// Estimate token usage for the entire message list.
		tc := token_counter.DefaultTokenCounter{}
		if counts, err := tc.CountToken(ctx, mi.Messages); err == nil {
			var total int64
			for _, c := range counts {
				total += c
			}
			attrs = append(attrs, A("estimated_tokens", total))
		}
	}

	// Try typed tool input.
	if ti := tool.ConvCallbackInput(input); ti != nil {
		attrs = append(attrs, A("arguments_json", truncate(ti.ArgumentsInJSON, 500)))
	}

	cb.log.Info(ctx, "callback.start", attrs...)
	return ctx
}

func (cb *LoggerCallback) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if cb.log == nil {
		return ctx
	}

	attrs := []Attr{
		A(AttrComponent, info.Type),
		A(AttrNodeName, info.Name),
	}

	// Try typed model output.
	if mo := model.ConvCallbackOutput(output); mo != nil {
		if mo.Message != nil {
			attrs = append(attrs, A("role", mo.Message.Role))
			attrs = append(attrs, A(AttrContentLength, len(mo.Message.Content)))
			attrs = append(attrs, A("content", truncate(mo.Message.Content, 500)))
			if len(mo.Message.ToolCalls) > 0 {
				attrs = append(attrs, A("tool_calls", len(mo.Message.ToolCalls)))
			}
		}
		if mo.TokenUsage != nil {
			attrs = append(attrs, A(AttrTokenPrompt, mo.TokenUsage.PromptTokens))
			attrs = append(attrs, A(AttrTokenCompletion, mo.TokenUsage.CompletionTokens))
			attrs = append(attrs, A(AttrTokenTotal, mo.TokenUsage.TotalTokens))
		}
	}

	// Try typed tool output.
	if to := tool.ConvCallbackOutput(output); to != nil {
		attrs = append(attrs, A("response", truncate(to.Response, 500)))
	}

	cb.log.Info(ctx, "callback.end", attrs...)
	return ctx
}

func (cb *LoggerCallback) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	if cb.log != nil {
		cb.log.Error(ctx, "callback.error",
			A(AttrComponent, info.Type), A(AttrNodeName, info.Name), A(AttrError, err.Error()))
	}
	return ctx
}

func (cb *LoggerCallback) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {

	var graphInfoName = react.GraphName

	go func() {
		defer func() {
			if err := recover(); err != nil {
				fmt.Println("[OnEndStream] panic err:", err)
			}
		}()

		defer output.Close()

		fmt.Println("=========[OnEndStream]=========")
		for {
			frame, err := output.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				fmt.Printf("internal error: %s\n", err)
				return
			}

			s, err := json.Marshal(frame)
			if err != nil {
				fmt.Printf("internal error: %s\n", err)
				return
			}

			if info.Name == graphInfoName {
				fmt.Printf("%s: %s\n", info.Name, string(s))
			}
		}

	}()
	return ctx
}

func (cb *LoggerCallback) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo,
	input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	defer input.Close()
	return ctx
}

// truncate shortens a string to maxLen runes, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "..."
}
