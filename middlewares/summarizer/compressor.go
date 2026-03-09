package summarizer

import (
	"context"
	"fmt"
	"strings"

	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"github.com/Pcapchu/Pcapchu/middlewares/token_counter"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// HistoryCompressor checks whether a list of text entries exceeds a token budget.
// If it does, it compresses the older entries via an LLM, keeping the most recent
// entries intact, and returns the compressed result.
//
// This is a generic component: the input and output are both []string, so it can
// be used for planner history, key findings, or any other accumulating text list.
type HistoryCompressor struct {
	counter      token_counter.TokenCounterImpl
	model        model.BaseChatModel
	systemPrompt string
	log          logger.Log

	// MaxTokens is the threshold that triggers compression.
	// Default: 32K tokens.
	MaxTokens int

	// KeepRecent is the number of recent entries to preserve uncompressed.
	// Default: 1.
	KeepRecent int
}

const (
	DefaultCompressorMaxTokens  = 32 * 1024
	DefaultCompressorKeepRecent = 1
)

// NewHistoryCompressor creates a new compressor.
func NewHistoryCompressor(m model.BaseChatModel, log logger.Log) *HistoryCompressor {
	return &HistoryCompressor{
		counter:      token_counter.DefaultTokenCounter{},
		model:        m,
		systemPrompt: defaultCompressorPrompt,
		log:          log,
		MaxTokens:    DefaultCompressorMaxTokens,
		KeepRecent:   DefaultCompressorKeepRecent,
	}
}

const defaultCompressorPrompt = `You are a summarization assistant. You will receive a list of text entries from previous investigation rounds.

Compress them into a single concise summary while following these rules:
- Preserve round numbers: tag findings with their source (e.g., "Round 1 found...").
- Preserve concrete entities: exact IPs, domains, timestamps, file paths, counts, query results.
- Preserve key findings: every important finding must survive compression.
- Track open questions: merge open questions across rounds; drop questions answered in later entries.
- Show chronological progression: how the investigation evolved.
- No fabrication: only include information present in the source entries.
- Do NOT drop quantitative data (packet counts, byte sizes, timestamps, durations).

Respond with ONLY the compressed summary text. No extra headers, XML tags, or commentary.`

// CompressResult holds what Compress returns.
type CompressResult struct {
	// Entries is the (possibly compressed) list of entries.
	Entries []string
	// Compressed is true if compression was actually performed.
	Compressed bool
	// CompressedUpTo is the index (0-based, exclusive) in the original slice
	// up to which entries were compressed. Only meaningful when Compressed=true.
	CompressedUpTo int
}

// Compress checks token count and compresses if necessary.
// Returns CompressResult with the new entries and metadata about what was compressed.
func (h *HistoryCompressor) Compress(ctx context.Context, entries []string) (*CompressResult, error) {
	if len(entries) == 0 {
		return &CompressResult{Entries: entries}, nil
	}

	// Count tokens
	counts, err := h.counter.CountStringTokens(ctx, entries)
	if err != nil {
		h.log.Warn(ctx, "history compressor: token count failed, skipping compression",
			logger.A(logger.AttrError, err.Error()))
		return &CompressResult{Entries: entries}, nil
	}

	var total int64
	for _, c := range counts {
		total += c
	}

	maxTokens := h.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultCompressorMaxTokens
	}

	if total <= int64(maxTokens) {
		return &CompressResult{Entries: entries}, nil
	}

	// Need compression. Keep the last KeepRecent entries intact.
	keepRecent := h.KeepRecent
	if keepRecent <= 0 {
		keepRecent = DefaultCompressorKeepRecent
	}
	if keepRecent >= len(entries) {
		// Nothing to compress — all entries are "recent"
		return &CompressResult{Entries: entries}, nil
	}

	olderEntries := entries[:len(entries)-keepRecent]
	recentEntries := entries[len(entries)-keepRecent:]

	h.log.Info(ctx, "history compressor: compressing",
		logger.A("total_tokens", total),
		logger.A("threshold", maxTokens),
		logger.A("older_count", len(olderEntries)),
		logger.A("keep_recent", len(recentEntries)))

	// Build the content to compress
	var sb strings.Builder
	for i, entry := range olderEntries {
		if i > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString(entry)
	}

	compressed, err := h.compress(ctx, sb.String())
	if err != nil {
		h.log.Error(ctx, "history compressor: LLM compression failed",
			logger.A(logger.AttrError, err.Error()))
		return &CompressResult{Entries: entries}, nil
	}

	// Build result: [compressed_summary, ...recent_entries]
	result := make([]string, 0, 1+len(recentEntries))
	result = append(result, compressed)
	result = append(result, recentEntries...)

	return &CompressResult{
		Entries:        result,
		Compressed:     true,
		CompressedUpTo: len(olderEntries),
	}, nil
}

func (h *HistoryCompressor) compress(ctx context.Context, content string) (string, error) {
	resp, err := h.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(h.systemPrompt),
		schema.UserMessage(content),
	})
	if err != nil {
		return "", fmt.Errorf("compress: %w", err)
	}
	return resp.Content, nil
}
