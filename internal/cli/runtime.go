package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
	otelShutdown func(context.Context)
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
	bashTool := tools.NewBashTool(env)
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

	exec := executor.NewExecutor(execAgent, log)

	return &runtime{
		ctx:          ctx,
		cancel:       cancel,
		log:          log,
		emitter:      emitter,
		env:          env,
		planner:      p,
		exec:         exec,
		otelShutdown: otelShutdown,
	}, nil
}

func (r *runtime) Close() {
	r.env.Cleanup(r.ctx)
	if r.otelShutdown != nil {
		r.otelShutdown(r.ctx)
	}
	r.emitter.Close()
	r.cancel()
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
	for round := startRound; round <= endRound; round++ {
		rt.log.Info(rt.ctx, fmt.Sprintf("========== Round %d/%d ==========", round, endRound))
		rt.log.Emit(events.TypeRoundStarted, events.RoundStartedData{
			Round:       round,
			TotalRounds: endRound,
		})

		// Load history from previous rounds
		var history *planner.PlannerInput
		if round > 1 {
			hist, err := store.LoadHistory(rt.ctx, sessionID)
			if err != nil {
				rt.log.Error(rt.ctx, "load history failed", logger.A(logger.AttrError, err.Error()))
				break
			}
			history = &planner.PlannerInput{
				UserQuery: query,
				PcapPath:  containerPcapPath,
				History:   hist,
			}
		}

		var planInput planner.PlannerInput
		if history != nil {
			planInput = *history
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

		result, err := rt.exec.Run(rt.ctx, plan, query, containerPcapPath, round)
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
			OpenQuestions:     result.OpenQuestions,
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
