package summarizer

import (
	"github.com/Pcapchu/Pcapchu/internal/prompts"
	"github.com/Pcapchu/Pcapchu/middlewares/token_counter"
	"github.com/cloudwego/eino/components/model"
)

// Config defines parameters for the summarization middleware.
//
// Required fields:
//   - Model: The language model used to generate summaries
//
// Optional fields:
//   - MaxTokensBeforeSummary: Trigger threshold (default: 128K)
//   - MaxTokensForRecentMessages: Recent message budget (default: 25K)
//   - Counter: Custom token counter (default: DefaultTokenCounter)
//   - SystemPrompt: Conversation summarization prompt (default: sum.md)
//   - ReportPrompt: Report summarization prompt (default: sum_report.md)
type Config struct {
	// MaxTokensBeforeSummary is the maximum token threshold before triggering summarization.
	MaxTokensBeforeSummary int

	// MaxTokensForRecentMessages is the token budget reserved for recent messages after summarization.
	MaxTokensForRecentMessages int

	// Counter is the token counter implementation.
	//
	// Optional. If nil, token_counter.DefaultTokenCounter is used.
	Counter token_counter.TokenCounterImpl

	// Model is the language model used to generate summaries.
	//
	// Required.
	Model model.BaseChatModel

	// SystemPrompt is the system prompt for conversation summarization.
	//
	// Optional. If empty, the embedded sum.md prompt is used.
	SystemPrompt string

	// ReportPrompt is the system prompt for report summarization.
	//
	// Optional. If empty, the embedded sum_report.md prompt is used.
	ReportPrompt string
}

// Defaults for conversation summarization.
const (
	// DefaultMaxTokensBeforeSummary is the default threshold for triggering summarization.
	// This represents approximately 128K tokens, suitable for most use cases.
	DefaultMaxTokensBeforeSummary = 128 * 1024

	// DefaultMaxTokensForRecentMessages is the default token budget for recent messages.
	// This is approximately 20% of the trigger threshold, balancing retention and compression.
	DefaultMaxTokensForRecentMessages = 25 * 1024
)

// Validate checks if the configuration is valid.
// Returns an error if required fields are missing or invalid.
func (c *Config) Validate() error {
	if c == nil {
		return ErrConfigNil
	}
	if c.Model == nil {
		return ErrModelRequired
	}
	return nil
}

// GetMaxTokensBeforeSummary returns the effective threshold, using default if not set.
func (c *Config) GetMaxTokensBeforeSummary() int {
	if c.MaxTokensBeforeSummary <= 0 {
		return DefaultMaxTokensBeforeSummary
	}
	return c.MaxTokensBeforeSummary
}

// GetMaxTokensForRecentMessages returns the effective recent message budget, using default if not set.
func (c *Config) GetMaxTokensForRecentMessages() int {
	if c.MaxTokensForRecentMessages <= 0 {
		return DefaultMaxTokensForRecentMessages
	}
	return c.MaxTokensForRecentMessages
}

// defaultConversationPrompt returns the embedded sum.md prompt.
func (c *Config) defaultConversationPrompt() string {
	return prompts.MustGet("sum")
}

// defaultReportPrompt returns the embedded sum_report.md prompt.
func (c *Config) defaultReportPrompt() string {
	return prompts.MustGet("sum_report")
}
