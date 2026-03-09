package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/Pcapchu/Pcapchu/internal/common"
	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/executor"
	"github.com/Pcapchu/Pcapchu/internal/planner"
	"github.com/Pcapchu/Pcapchu/internal/storage"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"github.com/Pcapchu/Pcapchu/middlewares/summarizer"
	"github.com/Pcapchu/Pcapchu/sandbox/environment"
	"github.com/Pcapchu/Pcapchu/sandbox/tools"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel"
)

// runtime bundles the long-lived components needed to run an investigation.
type runtime struct {
	ctx          context.Context
	cancel       context.CancelFunc
	log          logger.Log
	emitter      *events.ChannelEmitter
	env          environment.Env
	planner      *planner.Planner
	exec         *executor.Executor
	compressor   *summarizer.HistoryCompressor
	otelShutdown func(context.Context)
	cleanupOnce  sync.Once
}

func newRuntime(parentCtx context.Context) (*runtime, error) {
	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)

	// --- Logger + emitter ---
	emitter := events.NewChannelEmitter(1024)
	log := logger.NewLogger().
		WithSink(logger.NewPrettyConsoleSink()).
		WithEmit(func(eventType string, data any) {
			emitter.Emit(events.NewEvent(eventType, "", data))
		})
	logger.SetDefault(log)

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn(ctx, "[OTel] internal error", logger.A(logger.AttrError, err.Error()))
	}))

	var otelShutdown func(context.Context)
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		otelRes, err := logger.InitOTel(ctx, "pcapchu")
		if err != nil {
			log.Error(ctx, "otel init failed", logger.A(logger.AttrError, err.Error()))
		} else {
			log.WithSink(logger.NewOTelSink(otelRes.Tracer("pcapchu"), otelRes.Meter("pcapchu"), otelRes.Logger("pcapchu")))
			otelShutdown = func(ctx context.Context) { _ = otelRes.Shutdown(ctx) }
		}
	}

	// --- Event printer goroutine ---
	ch := emitter.Subscribe()
	go func() {
		for ev := range ch {
			fmt.Printf("[%s] %s: %s\n", ev.Timestamp.Format("15:04:05"), ev.Type, string(ev.Data))
		}
	}()

	// --- LLM ---
	apiKey := os.Getenv("OPENAI_API_KEY")
	modelName := os.Getenv("OPENAI_MODEL_NAME")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if apiKey == "" || modelName == "" {
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("OPENAI_API_KEY and OPENAI_MODEL_NAME environment variables are required")
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  apiKey,
		Model:   modelName,
		BaseURL: baseURL,
	})
	if err != nil {
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create chat model: %w", err)
	}

	// --- Docker sandbox ---
	log.Info(ctx, "creating docker sandbox...")
	env, err := environment.NewDockerEnv(ctx)
	if err != nil {
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create docker env: %w", err)
	}

	// --- Tools ---
	bashTool := tools.NewOutputGuard(tools.NewBashTool(env))
	sreTool := tools.NewSafeStrReplaceEditor(ctx, env)
	allTools := []tool.BaseTool{bashTool, sreTool}

	// --- Conversation summariser ---
	sumCfg := &summarizer.Config{Model: chatModel}
	convSummarizer, err := summarizer.NewDefaultConversationSummarizer(ctx, sumCfg, log)
	if err != nil {
		env.Cleanup(ctx)
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create conversation summarizer: %w", err)
	}

	// --- Logger callback ---
	loggerCB := logger.NewLoggerCallback(log)
	callbacks.AppendGlobalHandlers(loggerCB)

	// --- ReAct agents ---
	execAgent, err := newReActAgent(ctx, chatModel, allTools, 200, convSummarizer.SummarizeContext)
	if err != nil {
		env.Cleanup(ctx)
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create executor agent: %w", err)
	}

	plannerAgent, err := newReActAgent(ctx, chatModel, allTools, 15, nil)
	if err != nil {
		env.Cleanup(ctx)
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create planner agent: %w", err)
	}

	p, err := planner.NewPlanner(ctx, plannerAgent, log)
	if err != nil {
		env.Cleanup(ctx)
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create planner: %w", err)
	}

	exec := executor.NewExecutor(execAgent, chatModel, log)

	compressor := summarizer.NewHistoryCompressor(chatModel, log)

	rt := &runtime{
		ctx:          ctx,
		cancel:       cancel,
		log:          log,
		emitter:      emitter,
		env:          env,
		planner:      p,
		exec:         exec,
		compressor:   compressor,
		otelShutdown: otelShutdown,
	}

	// Ensure cleanup runs when the context is cancelled (e.g. Ctrl+C).
	// Close() is guarded by sync.Once so the deferred Close() in callers is safe.
	go func() {
		<-ctx.Done()
		rt.Close()
	}()

	return rt, nil
}

func (r *runtime) Close() {
	r.cleanupOnce.Do(func() {
		// Use a fresh context — r.ctx may already be cancelled by the signal.
		cleanupCtx := context.Background()
		r.log.Info(cleanupCtx, "cleaning up...")
		r.env.Cleanup(cleanupCtx)
		if r.otelShutdown != nil {
			r.otelShutdown(cleanupCtx)
		}
		r.emitter.Close()
		r.cancel()
	})
}

// copyPcapToContainer handles both file-path and DB-blob pcap sources.
func copyPcapToContainer(ctx context.Context, env environment.Env, sess *storage.Session, store *storage.Store, localPcapOverride string) (string, error) {
	if localPcapOverride != "" {
		dest := "/home/linuxbrew/" + filepath.Base(localPcapOverride)
		return dest, env.CopyFile(ctx, localPcapOverride, dest)
	}
	if sess.PcapFileID.Valid {
		data, err := store.GetPcapFileData(ctx, sess.PcapFileID.Int64)
		if err != nil {
			return "", fmt.Errorf("read pcap blob: %w", err)
		}
		dest := "/home/linuxbrew/capture.pcap"
		return dest, env.CopyReader(ctx, bytes.NewReader(data), dest, int64(len(data)))
	}
	if sess.PcapPath.Valid {
		dest := "/home/linuxbrew/" + filepath.Base(sess.PcapPath.String)
		return dest, env.CopyFile(ctx, sess.PcapPath.String, dest)
	}
	return "", fmt.Errorf("session has no pcap source")
}

// runInvestigation is the shared investigation loop used by both "analyze" and "session resume".
func runInvestigation(rt *runtime, store *storage.Store, sessionID, query, containerPcapPath string, startRound, endRound int) error {
	const (
		scopeKeyFindings    = "key_findings"
		scopePlannerHistory = "planner_history"
	)

	for round := startRound; round <= endRound; round++ {
		rt.log.Info(rt.ctx, fmt.Sprintf("========== Round %d/%d ==========", round, endRound))
		rt.log.Emit(events.TypeRoundStarted, events.RoundStartedData{
			Round:       round,
			TotalRounds: endRound,
		})

		// --- Collect and compress key findings ---
		keyFindingsHistory := ""
		if round > 1 {
			kfEntries, err := collectScopedEntries(rt.ctx, store, sessionID, scopeKeyFindings, func(r storage.Round) string {
				if r.KeyFindings == "" {
					return ""
				}
				return fmt.Sprintf("Round %d Key Findings:\n%s", r.Round, r.KeyFindings)
			})
			if err != nil {
				rt.log.Error(rt.ctx, "collect key findings failed", logger.A(logger.AttrError, err.Error()))
				break
			}
			if len(kfEntries) > 0 {
				compressed, err := compressAndSnapshot(rt, store, sessionID, scopeKeyFindings, kfEntries)
				if err != nil {
					rt.log.Warn(rt.ctx, "compress key findings failed, using raw", logger.A(logger.AttrError, err.Error()))
					keyFindingsHistory = strings.Join(kfEntries, "\n\n")
				} else {
					keyFindingsHistory = strings.Join(compressed, "\n\n")
				}
			}
		}

		// --- Collect and compress planner history ---
		var planInput planner.PlannerInput
		if round > 1 {
			histEntries, err := collectScopedEntries(rt.ctx, store, sessionID, scopePlannerHistory, func(r storage.Round) string {
				return formatRoundForPlanner(r)
			})
			if err != nil {
				rt.log.Error(rt.ctx, "collect planner history failed", logger.A(logger.AttrError, err.Error()))
				break
			}

			var historyText string
			if len(histEntries) > 0 {
				compressed, err := compressAndSnapshot(rt, store, sessionID, scopePlannerHistory, histEntries)
				if err != nil {
					rt.log.Warn(rt.ctx, "compress planner history failed, using raw", logger.A(logger.AttrError, err.Error()))
					historyText = strings.Join(histEntries, "\n\n---\n\n")
				} else {
					historyText = strings.Join(compressed, "\n\n---\n\n")
				}
			}

			// Build a SessionHistory from the (possibly compressed) text.
			// The compressed text replaces the Findings field; we still load the
			// most recent round's report for PreviousReport / AllReports.
			lastRounds, _ := store.LoadRoundsAfter(rt.ctx, sessionID, 0)
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

		plan, err := rt.planner.Run(rt.ctx, planInput)
		if err != nil {
			rt.log.Error(rt.ctx, "planner failed", logger.A(logger.AttrError, err.Error()), logger.A("round", round))
			break
		}
		rt.log.Info(rt.ctx, "plan created", logger.A("steps", len(plan.Steps)), logger.A("thought", plan.Thought))

		execQuery := plan.EnrichedInput
		if execQuery == "" {
			execQuery = query
		}
		result, err := rt.exec.Run(rt.ctx, plan, execQuery, containerPcapPath, round, keyFindingsHistory)
		if err != nil {
			rt.log.Error(rt.ctx, "executor failed", logger.A(logger.AttrError, err.Error()), logger.A("round", round))
			break
		}

		if err := store.SaveRound(rt.ctx, sessionID, storage.Round{
			Round:            round,
			ResearchFindings: result.Findings,
			OperationLog:     result.OperationLog,
			Summary:          result.Summary,
			KeyFindings:      result.KeyFindings,
			OpenQuestions:    result.OpenQuestions,
		}); err != nil {
			rt.log.Error(rt.ctx, "save round failed", logger.A(logger.AttrError, err.Error()))
			break
		}

		rt.log.Emit(events.TypeRoundCompleted, events.RoundCompletedData{
			Round:       round,
			Summary:     result.Summary,
			KeyFindings: result.KeyFindings,
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

// collectScopedEntries loads entries from a snapshot + remaining rounds for a given scope.
// The formatRound callback extracts the relevant text from each round; empty strings are skipped.
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

// compressAndSnapshot runs the compressor on entries and persists a snapshot if compression occurred.
// Returns the (possibly compressed) entries.
func compressAndSnapshot(rt *runtime, store *storage.Store, sessionID, scope string, entries []string) ([]string, error) {
	result, err := rt.compressor.Compress(rt.ctx, entries)
	if err != nil {
		return nil, err
	}

	if result.Compressed {
		// Determine the absolute round number the snapshot covers.
		snap, _ := store.LoadSnapshot(rt.ctx, sessionID, scope)
		baseRound := 0
		if snap != nil {
			baseRound = snap.CompressedUpTo
		}

		remainingRounds, _ := store.LoadRoundsAfter(rt.ctx, sessionID, baseRound)
		newCompressedUpTo := baseRound
		if result.CompressedUpTo > 0 && result.CompressedUpTo <= len(remainingRounds) {
			newCompressedUpTo = remainingRounds[result.CompressedUpTo-1].Round
		}

		if err := store.SaveSnapshot(rt.ctx, sessionID, scope, newCompressedUpTo, result.Entries[0]); err != nil {
			rt.log.Error(rt.ctx, "save snapshot failed",
				logger.A("scope", scope), logger.A(logger.AttrError, err.Error()))
		} else {
			rt.log.Info(rt.ctx, "history compressed",
				logger.A("scope", scope), logger.A("compressed_up_to_round", newCompressedUpTo))
		}
	}

	return result.Entries, nil
}

// formatRoundForPlanner builds a planner-history entry from a single round's data.
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

// buildSessionHistory constructs a SessionHistory from compressed history text
// and the full (uncompressed) list of all rounds (for PreviousReport / AllReports).
func buildSessionHistory(compressedText string, allRounds []storage.Round) *common.SessionHistory {
	hist := &common.SessionHistory{
		Findings: compressedText,
	}

	for _, r := range allRounds {
		rr := common.RoundReport{
			Round:        r.Round,
			Summary:      r.Summary,
			KeyFindings:  r.KeyFindings,
			OpenQuestions: r.OpenQuestions,
		}
		hist.AllReports = append(hist.AllReports, rr)
		hist.PreviousReport = &rr
	}
	return hist
}

// newReActAgent creates a react.Agent with the given tools, model, max steps,
// and optional message rewriter.
func newReActAgent(
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
