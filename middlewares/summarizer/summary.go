package summarizer

import (
	"context"
	"github.com/bytedance/gopkg/util/logger"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"strings"
)

type ContextSummarizerImpl interface {
	SummarizeContext(ctx context.Context, input []*schema.Message) []*schema.Message
}

type DefaultContextSummarizer struct {
	counter    func(ctx context.Context, msgs []adk.Message) (tokenNum []int64, err error)
	maxBefore  int
	maxRecent  int
	summarizer compose.Runnable[map[string]any, *schema.Message]
}

func NewDefaultContextSummarizer() *DefaultContextSummarizer {
	
}

// impl type MessageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
func (cs *DefaultContextSummarizer) SummarizeContext(ctx context.Context, input []*schema.Message) []*schema.Message {
	messages := input
	msgsToken, err := cs.counter(ctx, messages)
	if err != nil {
		logger.Fatalf("compress error")
		return input
	}
	if len(messages) != len(msgsToken) {
		logger.Fatalf("compress error")
		return input
	}

	var total int64
	for _, t := range msgsToken {
		total += t
	}
	// Trigger summarization only when exceeding threshold
	if total <= int64(cs.maxBefore) {
		return input
	}

	// Build blocks with user-messages, summary-message, tool-call pairings
	type block struct {
		msgs   []*schema.Message
		tokens int64
	}
	idx := 0

	systemBlock := block{}
	if idx < len(messages) {
		m := messages[idx]
		if m != nil && m.Role == schema.System {
			systemBlock.msgs = append(systemBlock.msgs, m)
			systemBlock.tokens += msgsToken[idx]
			idx++
		}
	}
	userBlock := block{}
	for idx < len(messages) {
		m := messages[idx]
		if m == nil {
			idx++
			continue
		}
		if m.Role != schema.User {
			break
		}
		userBlock.msgs = append(userBlock.msgs, m)
		userBlock.tokens += msgsToken[idx]
		idx++
	}
	summaryBlock := block{}
	if idx < len(messages) {
		m := messages[idx]
		if m != nil && m.Role == schema.Assistant {
			if _, ok := m.Extra[summaryMessageFlag]; ok {
				summaryBlock.msgs = append(summaryBlock.msgs, m)
				summaryBlock.tokens += msgsToken[idx]
				idx++
			}
		}
	}

	toolBlocks := make([]block, 0)
	for i := idx; i < len(messages); i++ {
		m := messages[i]
		if m == nil {
			continue
		}
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
			b := block{msgs: []*schema.Message{m}, tokens: msgsToken[i]}
			// Collect subsequent tool messages matching any tool call id
			callIDs := make(map[string]struct{}, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				callIDs[tc.ID] = struct{}{}
			}
			j := i + 1
			for j < len(messages) {
				nm := messages[j]
				if nm == nil || nm.Role != schema.Tool {
					break
				}
				// Match by ToolCallID when available; if empty, include but keep boundary
				if nm.ToolCallID == "" {
					b.msgs = append(b.msgs, nm)
					b.tokens += msgsToken[j]
				} else {
					if _, ok := callIDs[nm.ToolCallID]; !ok {
						// Tool message not belonging to this assistant call -> end pairing
						break
					}
					b.msgs = append(b.msgs, nm)
					b.tokens += msgsToken[j]
				}
				j++
			}
			toolBlocks = append(toolBlocks, b)
			i = j - 1
			continue
		}
		toolBlocks = append(toolBlocks, block{msgs: []*schema.Message{m}, tokens: msgsToken[i]})
	}

	// Split into recent and older within token budget, from newest to oldest
	var recentBlocks []block
	var olderBlocks []block
	var recentTokens int64
	for i := len(toolBlocks) - 1; i >= 0; i-- {
		b := toolBlocks[i]
		if recentTokens+b.tokens > int64(cs.maxRecent) {
			olderBlocks = append([]block{b}, olderBlocks...)
			continue
		}
		recentBlocks = append([]block{b}, recentBlocks...)
		recentTokens += b.tokens
	}

	joinBlocks := func(bs []block) string {
		var sb strings.Builder
		for _, b := range bs {
			for _, m := range b.msgs {
				sb.WriteString(renderMsg(m))
				sb.WriteString("\n")
			}
		}
		return sb.String()
	}

	olderText := joinBlocks(olderBlocks)
	recentText := joinBlocks(recentBlocks)

	msg, err := cs.summarizer.Invoke(ctx, map[string]any{
		"system_prompt":    joinBlocks([]block{systemBlock}),
		"user_messages":    joinBlocks([]block{userBlock}),
		"previous_summary": joinBlocks([]block{summaryBlock}),
		"older_messages":   olderText,
		"recent_messages":  recentText,
	})
	if err != nil {
		logger.Fatalf("compress error")
		return input
	}

	summaryMsg := schema.AssistantMessage(msg.Content, nil)
	summaryMsg.Name = "summary"
	summaryMsg.Extra = map[string]any{
		summaryMessageFlag: true,
	}

	// Build new state: prepend summary message, keep recent messages
	newMessages := make([]*schema.Message, 0, len(messages))
	newMessages = append(newMessages, systemBlock.msgs...)
	newMessages = append(newMessages, userBlock.msgs...)
	newMessages = append(newMessages, summaryMsg)
	for _, b := range recentBlocks {
		newMessages = append(newMessages, b.msgs...)
	}

	return newMessages

}

// Render messages into strings
func renderMsg(m *schema.Message) string {
	if m == nil {
		return ""
	}
	var sb strings.Builder
	if m.Role == schema.Tool {
		if m.ToolName != "" {
			sb.WriteString("[tool:")
			sb.WriteString(m.ToolName)
			sb.WriteString("]\n")
		} else {
			sb.WriteString("[tool]\n")
		}
	} else {
		sb.WriteString("[")
		sb.WriteString(string(m.Role))
		sb.WriteString("]\n")
	}
	if m.Content != "" {
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "" {
				sb.WriteString("tool_call: ")
				sb.WriteString(tc.Function.Name)
				sb.WriteString("\n")
			}
			if tc.Function.Arguments != "" {
				sb.WriteString("args: ")
				sb.WriteString(tc.Function.Arguments)
				sb.WriteString("\n")
			}
		}
	}
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
	return sb.String()
}
