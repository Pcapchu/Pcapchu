package investigation

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Pcapchu/Pcapchu/internal/common"
	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/executor"
	"github.com/Pcapchu/Pcapchu/internal/planner"
	"github.com/Pcapchu/Pcapchu/internal/storage"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"github.com/Pcapchu/Pcapchu/middlewares/summarizer"
	"github.com/Pcapchu/Pcapchu/sandbox/environment"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// CopyPcapToContainer handles both file-path and DB-blob pcap sources.
func CopyPcapToContainer(ctx context.Context, env environment.Env, sess *storage.Session, store *storage.Store, localPcapOverride string) (string, error) {
	if localPcapOverride != "" {
		dest := "/home/linuxbrew/" + filepath.Base(localPcapOverride)
		return dest, env.CopyFile(ctx, localPcapOverride, dest)
	}
	if sess.PcapFileID.Valid {
		data, err := store.GetPcapFileData(ctx, sess.PcapFileID.Int64)
		if err != nil {
			return "", fmt.Errorf("read pcap blob: %w", err)
		}
		filename := "capture.pcap"
		if name, err := store.GetPcapFilename(ctx, sess.PcapFileID.Int64); err == nil && name != "" {
			filename = name
		}
		dest := "/home/linuxbrew/" + filename
		return dest, env.CopyReader(ctx, bytes.NewReader(data), dest, int64(len(data)))
	}
	if sess.PcapPath.Valid {
		dest := "/home/linuxbrew/" + filepath.Base(sess.PcapPath.String)
		return dest, env.CopyFile(ctx, sess.PcapPath.String, dest)
	}
	return "", fmt.Errorf("session has no pcap source")
}

// OnRoundDone is called after each round completes successfully.
// The caller decides whether to persist the round immediately (CLI) or buffer it (server).
type OnRoundDone func(ctx context.Context, round storage.Round) error

// RunInvestigation is the shared investigation loop used by both CLI and server.
func RunInvestigation(
	ctx context.Context,
	log logger.Log,
	p *planner.Planner,
	exec *executor.Executor,
	comp *summarizer.HistoryCompressor,
	store *storage.Store,
	sessionID, query, containerPcapPath string,
	startRound, endRound int,
	onRoundDone OnRoundDone,
) error {
	const (
		scopeKeyFindings    = "key_findings"
		scopePlannerHistory = "planner_history"
	)

	for round := startRound; round <= endRound; round++ {
		log.Info(ctx, fmt.Sprintf("========== Round %d/%d ==========", round, endRound))
		log.Emit(events.TypeRoundStarted, events.RoundStartedData{
			Round:       round,
			TotalRounds: endRound,
		})

		// --- Collect and compress key findings ---
		keyFindingsHistory := ""
		if round > 1 {
			kfEntries, err := collectScopedEntries(ctx, store, sessionID, scopeKeyFindings, func(r storage.Round) string {
				if r.KeyFindings == "" {
					return ""
				}
				return fmt.Sprintf("Round %d Key Findings:\n%s", r.Round, r.KeyFindings)
			})
			if err != nil {
				log.Error(ctx, "collect key findings failed", logger.A(logger.AttrError, err.Error()))
				break
			}
			if len(kfEntries) > 0 {
				compressed, err := compressAndSnapshot(ctx, comp, log, store, sessionID, scopeKeyFindings, kfEntries)
				if err != nil {
					log.Warn(ctx, "compress key findings failed, using raw", logger.A(logger.AttrError, err.Error()))
					keyFindingsHistory = strings.Join(kfEntries, "\n\n")
				} else {
					keyFindingsHistory = strings.Join(compressed, "\n\n")
				}
			}
		}

		// --- Collect and compress planner history ---
		var planInput planner.PlannerInput
		if round > 1 {
			histEntries, err := collectScopedEntries(ctx, store, sessionID, scopePlannerHistory, func(r storage.Round) string {
				return formatRoundForPlanner(r)
			})
			if err != nil {
				log.Error(ctx, "collect planner history failed", logger.A(logger.AttrError, err.Error()))
				break
			}

			var historyText string
			if len(histEntries) > 0 {
				compressed, err := compressAndSnapshot(ctx, comp, log, store, sessionID, scopePlannerHistory, histEntries)
				if err != nil {
					log.Warn(ctx, "compress planner history failed, using raw", logger.A(logger.AttrError, err.Error()))
					historyText = strings.Join(histEntries, "\n\n---\n\n")
				} else {
					historyText = strings.Join(compressed, "\n\n---\n\n")
				}
			}

			lastRounds, _ := store.LoadRoundsAfter(ctx, sessionID, 0)
			hist := buildSessionHistory(historyText, lastRounds)

			planInput = planner.PlannerInput{
				UserQuery: query,
				PcapPath:  containerPcapPath,
				History:   hist,
			}
		} else {
			planInput = planner.PlannerInput{
				UserQuery: query,
				PcapPath:  containerPcapPath,
			}
		}

		plan, err := p.Run(ctx, planInput)
		if err != nil {
			log.Error(ctx, "planner failed", logger.A(logger.AttrError, err.Error()), logger.A("round", round))
			break
		}
		log.Info(ctx, "plan created", logger.A("steps", len(plan.Steps)), logger.A("thought", plan.Thought))

		execQuery := plan.EnrichedInput
		if execQuery == "" {
			execQuery = query
		}
		result, err := exec.Run(ctx, plan, execQuery, containerPcapPath, round, keyFindingsHistory)
		if err != nil {
			log.Error(ctx, "executor failed", logger.A(logger.AttrError, err.Error()), logger.A("round", round))
			break
		}

		roundData := storage.Round{
			Round:            round,
			UserQuery:        query,
			ResearchFindings: result.Findings,
			OperationLog:     result.OperationLog,
			Summary:          result.Summary,
			KeyFindings:      result.KeyFindings,
			OpenQuestions:     result.OpenQuestions,
			MarkdownReport:   result.MarkdownReport,
		}

		if onRoundDone != nil {
			if err := onRoundDone(ctx, roundData); err != nil {
				log.Error(ctx, "onRoundDone failed", logger.A(logger.AttrError, err.Error()))
				break
			}
		}

		log.Emit(events.TypeRoundCompleted, events.RoundCompletedData{
			Round:          round,
			Summary:        result.Summary,
			KeyFindings:    result.KeyFindings,
			MarkdownReport: result.MarkdownReport,
		})

		fmt.Printf("\n===== Round %d Summary =====\n", round)
		fmt.Printf("Summary: %s\n", result.Summary)
		fmt.Printf("Key Findings: %s\n", result.KeyFindings)
		if result.OpenQuestions != "" {
			fmt.Printf("Open Questions: %s\n", result.OpenQuestions)
		}
		fmt.Println()
	}
	return nil
}

// NewReActAgent creates a react.Agent with the given tools, model, max steps,
// and optional message rewriter.
func NewReActAgent(
	ctx context.Context,
	chatModel *openai.ChatModel,
	allTools []tool.BaseTool,
	maxStep int,
	rewriter func(ctx context.Context, msgs []*schema.Message) []*schema.Message,
) (*react.Agent, error) {
	cfg := &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: allTools,
		},
		MaxStep: maxStep,
	}
	if rewriter != nil {
		cfg.MessageRewriter = rewriter
	}
	return react.NewAgent(ctx, cfg)
}

// --- internal helpers ---

func collectScopedEntries(ctx context.Context, store *storage.Store, sessionID, scope string, formatRound func(storage.Round) string) ([]string, error) {
	snap, err := store.LoadSnapshot(ctx, sessionID, scope)
	if err != nil {
		return nil, fmt.Errorf("load snapshot (%s): %w", scope, err)
	}

	afterRound := 0
	if snap != nil {
		afterRound = snap.CompressedUpTo
	}

	rounds, err := store.LoadRoundsAfter(ctx, sessionID, afterRound)
	if err != nil {
		return nil, fmt.Errorf("load rounds after %d (%s): %w", afterRound, scope, err)
	}

	var entries []string
	if snap != nil && snap.Content != "" {
		entries = append(entries, snap.Content)
	}
	for _, r := range rounds {
		if text := formatRound(r); text != "" {
			entries = append(entries, text)
		}
	}
	return entries, nil
}

func compressAndSnapshot(ctx context.Context, comp *summarizer.HistoryCompressor, log logger.Log, store *storage.Store, sessionID, scope string, entries []string) ([]string, error) {
	result, err := comp.Compress(ctx, entries)
	if err != nil {
		return nil, err
	}

	if result.Compressed {
		snap, _ := store.LoadSnapshot(ctx, sessionID, scope)
		baseRound := 0
		if snap != nil {
			baseRound = snap.CompressedUpTo
		}

		remainingRounds, _ := store.LoadRoundsAfter(ctx, sessionID, baseRound)
		newCompressedUpTo := baseRound
		if result.CompressedUpTo > 0 && result.CompressedUpTo <= len(remainingRounds) {
			newCompressedUpTo = remainingRounds[result.CompressedUpTo-1].Round
		}

		if err := store.SaveSnapshot(ctx, sessionID, scope, newCompressedUpTo, result.Entries[0]); err != nil {
			log.Error(ctx, "save snapshot failed",
				logger.A("scope", scope), logger.A(logger.AttrError, err.Error()))
		} else {
			log.Info(ctx, "history compressed",
				logger.A("scope", scope), logger.A("compressed_up_to_round", newCompressedUpTo))
		}
	}

	return result.Entries, nil
}

func formatRoundForPlanner(r storage.Round) string {
	var parts []string
	if r.ResearchFindings != "" {
		parts = append(parts, fmt.Sprintf("### Research Findings\n%s", r.ResearchFindings))
	}
	if r.Summary != "" {
		parts = append(parts, fmt.Sprintf("### Summary\n%s", r.Summary))
	}
	if r.KeyFindings != "" {
		parts = append(parts, fmt.Sprintf("### Key Findings\n%s", r.KeyFindings))
	}
	if r.OpenQuestions != "" {
		parts = append(parts, fmt.Sprintf("### Open Questions\n%s", r.OpenQuestions))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("## Round %d\n\n%s", r.Round, strings.Join(parts, "\n\n"))
}

func buildSessionHistory(compressedText string, allRounds []storage.Round) *common.SessionHistory {
	hist := &common.SessionHistory{
		Findings: compressedText,
	}

	for _, r := range allRounds {
		rr := common.RoundReport{
			Round:          r.Round,
			Summary:        r.Summary,
			KeyFindings:    r.KeyFindings,
			OpenQuestions:  r.OpenQuestions,
			MarkdownReport: r.MarkdownReport,
		}
		hist.AllReports = append(hist.AllReports, rr)
		hist.PreviousReport = &rr
	}
	return hist
}
