package main

import (
	"context"
	"flag"
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
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
)

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

func main() {
	// --- CLI flags ---
	var (
		pcapFile string
		query    string
		rounds   int
		dbPath   string
	)
	flag.StringVar(&pcapFile, "pcap", "", "path to local .pcap file (required)")
	flag.StringVar(&query, "query", "Analyze this pcap file and identify any security concerns.", "analysis query")
	flag.IntVar(&rounds, "rounds", 1, "number of investigation rounds")
	flag.StringVar(&dbPath, "db", "./pcapchu.db", "SQLite database path")
	flag.Parse()

	if pcapFile == "" {
		fmt.Fprintln(os.Stderr, "usage: pcapchu -pcap <file> [-query <q>] [-rounds <n>]")
		return
	}

	if err := run(pcapFile, query, rounds, dbPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
}

func run(pcapFile, query string, rounds int, dbPath string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// --- Logger ---
	emitter := events.NewChannelEmitter(1024)
	defer emitter.Close()

	log := logger.NewLogger().
		WithSink(logger.NewPrettyConsoleSink()).
		WithEmit(func(eventType string, data any) {
			emitter.Emit(events.NewEvent(eventType, "", data))
		})
	logger.SetDefault(log)

	// OTel global error handler — surface internal errors instead of silencing them
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn(ctx, "[OTel] internal error", logger.A(logger.AttrError, err.Error()))
	}))

	// --- OTel (enabled when OTEL_EXPORTER_OTLP_ENDPOINT is set) ---
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		otelRes, err := logger.InitOTel(ctx, "pcapchu")
		if err != nil {
			log.Error(ctx, "otel init failed", logger.A(logger.AttrError, err.Error()))
		} else {
			defer otelRes.Shutdown(ctx)
			log.WithSink(logger.NewOTelSink(otelRes.Tracer("pcapchu"), otelRes.Meter("pcapchu"), otelRes.Logger("pcapchu")))
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
		return fmt.Errorf("OPENAI_API_KEY and OPENAI_MODEL_NAME environment variables are required")
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  apiKey,
		Model:   modelName,
		BaseURL: baseURL,
	})
	if err != nil {
		return fmt.Errorf("create chat model: %w", err)
	}

	// --- Docker sandbox ---
	log.Info(ctx, "creating docker sandbox...")
	env, err := environment.NewDockerEnv(ctx)
	if err != nil {
		return fmt.Errorf("create docker env: %w", err)
	}
	defer env.Cleanup(ctx)

	// Copy pcap file into container
	containerPcapPath := "/home/linuxbrew/" + filepath.Base(pcapFile)
	if err := env.CopyFile(ctx, pcapFile, containerPcapPath); err != nil {
		return fmt.Errorf("copy pcap to container: %w", err)
	}
	log.Info(ctx, "pcap copied to container", logger.A("path", containerPcapPath))

	// --- Tools ---
	bashTool := tools.NewBashTool(env)
	sreTool := tools.NewSafeStrReplaceEditor(ctx, env)
	allTools := []tool.BaseTool{
		bashTool,
		sreTool,
	}

	// --- Conversation summarizer (message rewriter for react agent) ---
	sumCfg := &summarizer.Config{Model: chatModel}
	convSummarizer, err := summarizer.NewDefaultConversationSummarizer(ctx, sumCfg, log)
	if err != nil {
		return fmt.Errorf("create conversation summarizer: %w", err)
	}

	// --- Logger callback for eino graph ---
	loggerCB := logger.NewLoggerCallback(log)
	callbacks.AppendGlobalHandlers(loggerCB)

	// --- ReAct agents ---
	execAgent, err := newReActAgent(ctx, chatModel, allTools, 200, convSummarizer.SummarizeContext)
	if err != nil {
		return fmt.Errorf("create executor agent: %w", err)
	}

	plannerAgent, err := newReActAgent(ctx, chatModel, allTools, 15, nil)
	if err != nil {
		return fmt.Errorf("create planner agent: %w", err)
	}

	p, err := planner.NewPlanner(ctx, plannerAgent, log)
	if err != nil {
		return fmt.Errorf("create planner: %w", err)
	}

	// --- Executor ---
	exec := executor.NewExecutor(execAgent, log)

	// --- Storage ---
	store, err := storage.New(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	sessionID := uuid.New().String()
	if err := store.CreateSession(ctx, sessionID, query, pcapFile); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// --- Investigation loop ---
	for round := 1; round <= rounds; round++ {
		log.Info(ctx, fmt.Sprintf("========== Round %d/%d ==========", round, rounds))

		// Load history from previous rounds
		var history *planner.PlannerInput
		if round > 1 {
			hist, err := store.LoadHistory(ctx, sessionID)
			if err != nil {
				log.Error(ctx, "load history failed", logger.A(logger.AttrError, err.Error()))
				break
			}
			history = &planner.PlannerInput{
				UserQuery: query,
				PcapPath:  containerPcapPath,
				History:   hist,
			}
		}

		// Plan
		var planInput planner.PlannerInput
		if history != nil {
			planInput = *history
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

		// Execute
		result, err := exec.Run(ctx, plan, query, containerPcapPath, round)
		if err != nil {
			log.Error(ctx, "executor failed", logger.A(logger.AttrError, err.Error()), logger.A("round", round))
			break
		}

		// Save round
		if err := store.SaveRound(ctx, sessionID, storage.RoundRecord{
			Round:            round,
			ResearchFindings: result.Findings,
			OperationLog:     result.OperationLog,
			Summary:          result.Summary,
			KeyFindings:      result.KeyFindings,
			OpenQuestions:    result.OpenQuestions,
		}); err != nil {
			log.Error(ctx, "save round failed", logger.A(logger.AttrError, err.Error()))
			break
		}

		// Print round summary
		fmt.Printf("\n===== Round %d Summary =====\n", round)
		fmt.Printf("Summary: %s\n", result.Summary)
		fmt.Printf("Key Findings: %s\n", result.KeyFindings)
		if result.OpenQuestions != "" {
			fmt.Printf("Open Questions: %s\n", result.OpenQuestions)
		}
		fmt.Println()
	}

	log.Info(ctx, "investigation complete", logger.A("session_id", sessionID))
	return nil
}
