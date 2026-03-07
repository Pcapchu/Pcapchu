package summarizer

import (
	"context"
	"fmt"
	"strings"

	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"github.com/Pcapchu/Pcapchu/middlewares/token_counter"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// ConversationSummarizer compresses conversation message history when it exceeds the token budget.
type ConversationSummarizer interface {
	SummarizeContext(ctx context.Context, input []*schema.Message) []*schema.Message
}

// ReportSummarizer compresses accumulated round reports.
type ReportSummarizer interface {
	SummarizeText(ctx context.Context, text string, previousSummary string) (string, error)
}

// DefaultConversationSummarizer implements ConversationSummarizer with configurable token budgets and LLM-based compression.
type DefaultConversationSummarizer struct {
	counter    token_counter.TokenCounterImpl
	maxBefore  int
	maxRecent  int
	summarizer compose.Runnable[map[string]any, *schema.Message]
	log        logger.Log
}

// NewDefaultConversationSummarizer creates a conversation summarizer.
func NewDefaultConversationSummarizer(ctx context.Context, cfg *Config, log logger.Log) (*DefaultConversationSummarizer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = cfg.defaultConversationPrompt()
	}

	compiled, err := buildSummarizerGraph(ctx, sysPrompt, cfg.Model)
	if err != nil {
		return nil, err
	}

	counter := cfg.Counter
	if counter == nil {
		counter = token_counter.DefaultTokenCounter{}
	}

	return &DefaultConversationSummarizer{
		counter:    counter,
		maxBefore:  cfg.GetMaxTokensBeforeSummary(),
		maxRecent:  cfg.GetMaxTokensForRecentMessages(),
		summarizer: compiled,
		log:        log,
	}, nil
}

// DefaultReportSummarizer implements ReportSummarizer for compressing round reports.
type DefaultReportSummarizer struct {
	model        model.BaseChatModel
	systemPrompt string
	log          logger.Log
}

// NewDefaultReportSummarizer creates a report summarizer.
func NewDefaultReportSummarizer(cfg *Config, log logger.Log) (*DefaultReportSummarizer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	sysPrompt := cfg.ReportPrompt
	if sysPrompt == "" {
		sysPrompt = cfg.defaultReportPrompt()
	}

	return &DefaultReportSummarizer{
		model:        cfg.Model,
		systemPrompt: sysPrompt,
		log:          log,
	}, nil
}

// SummarizeText compresses report text using the LLM.
// If previousSummary is non-empty, the model merges old and new content.
func (rs *DefaultReportSummarizer) SummarizeText(ctx context.Context, text string, previousSummary string) (string, error) {
	content := text
	if previousSummary != "" {
		content = fmt.Sprintf("## Previous Compressed Summary\n\n%s\n\n---\n\n## New Reports to Integrate\n\n%s", previousSummary, text)
	}

	resp, err := rs.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(rs.systemPrompt),
		schema.UserMessage(content),
	})
	if err != nil {
		rs.log.Error(ctx, "report summarizer: LLM invocation failed", logger.A(logger.AttrError, err.Error()))
		return "", fmt.Errorf("summarize reports: %w", err)
	}
	return resp.Content, nil
}

// buildSummarizerGraph builds the template → model graph used for conversation summarization.
func buildSummarizerGraph(ctx context.Context, sysPrompt string, m model.BaseChatModel) (compose.Runnable[map[string]any, *schema.Message], error) {
	tpl := prompt.FromMessages(schema.GoTemplate,
		schema.SystemMessage(sysPrompt),
		schema.UserMessage("{{.user_content}}"),
	)

	modelLambda := compose.InvokableLambda(func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
		return m.Generate(ctx, in)
	})

	g := compose.NewGraph[map[string]any, *schema.Message]()
	_ = g.AddChatTemplateNode("template", tpl)
	_ = g.AddLambdaNode("model", modelLambda)
	_ = g.AddEdge(compose.START, "template")
	_ = g.AddEdge("template", "model")
	_ = g.AddEdge("model", compose.END)

	compiled, err := g.Compile(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile summarizer graph: %w", err)
	}
	return compiled, nil
}

// SummarizeContext compresses conversation history when token count exceeds the budget.
// impl type MessageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
func (cs *DefaultConversationSummarizer) SummarizeContext(ctx context.Context, input []*schema.Message) []*schema.Message {
	messages := input
	msgsToken, err := cs.counter.CountToken(ctx, messages)
	if err != nil {
		cs.log.Error(ctx, "summarizer: token counting failed", logger.A(logger.AttrError, err.Error()))
		return input
	}
	if len(messages) != len(msgsToken) {
		cs.log.Error(ctx, "summarizer: token count mismatch", logger.A("messages", len(messages)), logger.A("counts", len(msgsToken)))
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
