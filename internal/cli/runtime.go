package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/executor"
	"github.com/Pcapchu/Pcapchu/internal/investigation"
	"github.com/Pcapchu/Pcapchu/internal/planner"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"github.com/Pcapchu/Pcapchu/middlewares/summarizer"
	"github.com/Pcapchu/Pcapchu/sandbox/environment"
	"github.com/Pcapchu/Pcapchu/sandbox/tools"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
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
	execAgent, err := investigation.NewReActAgent(ctx, chatModel, allTools, 200, convSummarizer.SummarizeContext)
	if err != nil {
		env.Cleanup(ctx)
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create executor agent: %w", err)
	}

	plannerAgent, err := investigation.NewReActAgent(ctx, chatModel, allTools, 15, nil)
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
