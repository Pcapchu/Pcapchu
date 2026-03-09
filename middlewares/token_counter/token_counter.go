package token_counter

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkoukk/tiktoken-go"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type TokenCounterImpl interface {
	CountToken(ctx context.Context, msgs []adk.Message) (tokenNum []int64, err error)
	CountStringTokens(ctx context.Context, texts []string) (tokenNum []int64, err error)
}

type DefaultTokenCounter struct {
}

// defaultCounterToken is the default token counter using tiktoken's cl100k_base encoding.
//
// This encoding is compatible with OpenAI models and provides accurate token counts.
// It counts tokens for:
//   - Message role
//   - Main content
//   - Multi-modal text parts (user input and assistant output)
//   - Tool call function names and arguments
func (tc DefaultTokenCounter) CountToken(ctx context.Context, msgs []adk.Message) (tokenNum []int64, err error) {
	const encoding = "cl100k_base"
	tkt, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, fmt.Errorf("get encoding failed, encoding=%v, err=%w", encoding, err)
	}

	tokenNum = make([]int64, len(msgs))

	for i, m := range msgs {
		if m == nil {
			tokenNum[i] = 0
			continue
		}

		var sb strings.Builder

		// Message role contributes to chat tokenization overhead
		if m.Role != "" {
			sb.WriteString(string(m.Role))
			sb.WriteString("\n")
		}

		// Core text content
		if m.Content != "" {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}

		// Multi modal input/output text parts
		for _, part := range m.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
				sb.WriteString(part.Text)
				sb.WriteString("\n")
			}
		}
		for _, part := range m.AssistantGenMultiContent {
			if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
				sb.WriteString(part.Text)
				sb.WriteString("\n")
			}
		}

		// Tool call textual context (name + arguments)
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "" {
				sb.WriteString(tc.Function.Name)
				sb.WriteString("\n")
			}
			if tc.Function.Arguments != "" {
				sb.WriteString(tc.Function.Arguments)
				sb.WriteString("\n")
			}
		}

		text := sb.String()
		if text == "" {
			tokenNum[i] = 0
			continue
		}

		tokens := tkt.Encode(text, nil, nil)
		tokenNum[i] = int64(len(tokens))
	}

	return tokenNum, nil
}

// CountStringTokens counts tokens for plain text strings.
func (tc DefaultTokenCounter) CountStringTokens(_ context.Context, texts []string) ([]int64, error) {
	const encoding = "cl100k_base"
	tkt, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, fmt.Errorf("get encoding failed, encoding=%v, err=%w", encoding, err)
	}
	out := make([]int64, len(texts))
	for i, t := range texts {
		out[i] = int64(len(tkt.Encode(t, nil, nil)))
	}
	return out, nil
}
