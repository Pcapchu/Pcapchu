package summarizer

import (
	"github.com/cloudwego/eino/components/model"
)

// Config defines parameters for the conversation summarization middleware.
//
// It controls when summarization is triggered, how much recent context is retained,
// and how the summarization is performed.
//
// Required fields:
//   - Model: The language model used to generate summaries
//
// Optional fields:
//   - MaxTokensBeforeSummary: Trigger threshold (default: 128K)
//   - MaxTokensForRecentMessages: Recent message budget (default: 25K)
//   - Counter: Custom token counter (default: cl100k_base encoding)
//   - SystemPrompt: Summarization prompt (default: built-in prompt)
type Config struct {
	// MaxTokensBeforeSummary is the maximum token threshold before triggering summarization.
	//
	// When the total token count of system prompt + conversation history exceeds this value,
	// the middleware will initiate summarization of older messages.
	//
	// Default: DefaultMaxTokensBeforeSummary (128 * 1024 = 131,072 tokens)
	// Set to 0 or negative to use default.
	MaxTokensBeforeSummary int

	// MaxTokensForRecentMessages is the token budget reserved for recent messages after summarization.
	//
	// After summarization, at most this many tokens of recent messages (counted from newest to oldest)
	// will be retained in full detail. Older messages are condensed into a single summary message.
	//
	// Default: DefaultMaxTokensForRecentMessages (25 * 1024 = 25,600 tokens)
	// Set to 0 or negative to use default.
	MaxTokensForRecentMessages int

	// Counter is a custom function for counting tokens in messages.
	//
	// Optional. If nil, the middleware uses defaultCounterToken which employs
	// the cl100k_base encoding (OpenAI's tokenization).
	Counter TokenCounter

	// Model is the language model used to generate summaries.
	//
	// Required. This model will be invoked to summarize older conversation messages.
	// Typically, you can use the same model as your main agent, but you may also
	// use a smaller/faster model for cost efficiency.
	Model model.BaseChatModel

	// SystemPrompt is the system prompt for the summarizer model.
	//
	// Optional. If empty, PromptOfSummary (the built-in prompt) is used.
	SystemPrompt string
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
