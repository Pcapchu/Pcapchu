package investigation

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/executor"
	"github.com/Pcapchu/Pcapchu/internal/planner"
	"github.com/Pcapchu/Pcapchu/middlewares/httpclient"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"github.com/Pcapchu/Pcapchu/middlewares/summarizer"
	"github.com/Pcapchu/Pcapchu/sandbox/environment"
	"github.com/Pcapchu/Pcapchu/sandbox/tools"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Runtime bundles the components needed to run an investigation.
// Both CLI and server create a Runtime per investigation.
type Runtime struct {
	ctx         context.Context
	cancel      context.CancelFunc
	log         logger.Log
	emitter     *events.ChannelEmitter
	env         environment.Env
	planner     *planner.Planner
	exec        *executor.Executor
	compressor  *summarizer.HistoryCompressor
	langHint    *LangHintModifier
	cleanupOnce sync.Once
}

// NewRuntime creates a fully initialised Runtime.
// The caller provides a configured Log (with sinks and EmitFunc already wired)
// and a ChannelEmitter for event distribution.
func NewRuntime(parentCtx context.Context, log logger.Log, emitter *events.ChannelEmitter) (*Runtime, error) {
	ctx, cancel := context.WithCancel(parentCtx)

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
		APIKey:     apiKey,
		Model:      modelName,
		BaseURL:    baseURL,
		HTTPClient: httpclient.NewResilientClient(),
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
	convSummarizer, err := summarizer.NewDefaultConversationSummarizer(ctx, &summarizer.Config{Model: chatModel}, log)
	if err != nil {
		env.Cleanup(ctx)
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create conversation summarizer: %w", err)
	}

	// --- Logger callback ---
	loggerCB := logger.NewLoggerCallback(log)
	callbacks.InitCallbackHandlers([]callbacks.Handler{loggerCB})
	// callbacks.AppendGlobalHandlers(loggerCB)

	// --- Language hint modifier ---
	langHint := &LangHintModifier{}

	// Compose rewriters: language hint first, then conversation summariser.
	execRewriter := func(ctx context.Context, msgs []*schema.Message) []*schema.Message {
		msgs = langHint.Rewrite(ctx, msgs)
		return convSummarizer.SummarizeContext(ctx, msgs)
	}

	// --- ReAct agents ---
	execAgent, err := NewReActAgent(ctx, chatModel, allTools, 200, execRewriter)
	if err != nil {
		env.Cleanup(ctx)
		cancel()
		emitter.Close()
		return nil, fmt.Errorf("create executor agent: %w", err)
	}

	plannerAgent, err := NewReActAgent(ctx, chatModel, allTools, 200, langHint.Rewrite)
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

	return &Runtime{
		ctx:        ctx,
		cancel:     cancel,
		log:        log,
		emitter:    emitter,
		env:        env,
		planner:    p,
		exec:       exec,
		compressor: compressor,
		langHint:   langHint,
	}, nil
}

// Accessors

func (r *Runtime) Ctx() context.Context                      { return r.ctx }
func (r *Runtime) Log() logger.Log                           { return r.log }
func (r *Runtime) Emitter() *events.ChannelEmitter           { return r.emitter }
func (r *Runtime) Env() environment.Env                      { return r.env }
func (r *Runtime) Planner() *planner.Planner                 { return r.planner }
func (r *Runtime) Exec() *executor.Executor                  { return r.exec }
func (r *Runtime) Compressor() *summarizer.HistoryCompressor { return r.compressor }

// SetUserQuery updates the language hint based on the user's query language.
func (r *Runtime) SetUserQuery(query string) { r.langHint.SetQuery(query) }

// Close tears down the runtime. Safe to call multiple times.
func (r *Runtime) Close() {
	r.cleanupOnce.Do(func() {
		r.env.Cleanup(context.Background())
		r.emitter.Close()
		r.cancel()
	})
}
