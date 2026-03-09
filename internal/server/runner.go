package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/executor"
	"github.com/Pcapchu/Pcapchu/internal/investigation"
	"github.com/Pcapchu/Pcapchu/internal/planner"
	"github.com/Pcapchu/Pcapchu/internal/storage"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"github.com/Pcapchu/Pcapchu/middlewares/summarizer"
	"github.com/Pcapchu/Pcapchu/sandbox/environment"
	"github.com/Pcapchu/Pcapchu/sandbox/tools"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
)

// numberedEvent pairs a sequence number with an event for SSE broadcast.
type numberedEvent struct {
	Seq   int
	Event events.Event
}

// activeRun tracks a running investigation session.
type activeRun struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when the investigation goroutine exits

	mu      sync.Mutex
	clients []chan numberedEvent
	seq     int // monotonic, guarded by mu
}

func (ar *activeRun) addClient() chan numberedEvent {
	ch := make(chan numberedEvent, 256)
	ar.mu.Lock()
	ar.clients = append(ar.clients, ch)
	ar.mu.Unlock()
	return ch
}

func (ar *activeRun) removeClient(ch chan numberedEvent) {
	ar.mu.Lock()
	for i, c := range ar.clients {
		if c == ch {
			ar.clients = append(ar.clients[:i], ar.clients[i+1:]...)
			break
		}
	}
	ar.mu.Unlock()
}

func (ar *activeRun) broadcast(store *storage.Store, sessionID string, ev events.Event) {
	ar.mu.Lock()
	ar.seq++
	seq := ar.seq
	data := "{}"
	if ev.Data != nil {
		data = string(ev.Data)
	}
	for _, ch := range ar.clients {
		select {
		case ch <- numberedEvent{Seq: seq, Event: ev}:
		default: // slow client — drop
		}
	}
	ar.mu.Unlock()

	// Persist event for replay (best-effort).
	_ = store.SaveEvent(context.Background(), sessionID, seq, ev.Type, data)
}

func (ar *activeRun) closeClients() {
	ar.mu.Lock()
	for _, ch := range ar.clients {
		close(ch)
	}
	ar.clients = nil
	ar.mu.Unlock()
}

// Runner manages concurrent investigation sessions.
type Runner struct {
	store *storage.Store
	log   logger.Log

	mu     sync.RWMutex
	active map[string]*activeRun
}

// NewRunner creates a Runner.
func NewRunner(store *storage.Store, log logger.Log) *Runner {
	return &Runner{
		store:  store,
		log:    log,
		active: make(map[string]*activeRun),
	}
}

// GetActive returns the activeRun for a session, or nil if not running.
func (r *Runner) GetActive(sessionID string) *activeRun {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active[sessionID]
}

// IsRunning reports whether a session has an active investigation.
func (r *Runner) IsRunning(sessionID string) bool {
	return r.GetActive(sessionID) != nil
}

// Cancel cancels the active investigation for a session.
func (r *Runner) Cancel(sessionID string) bool {
	r.mu.RLock()
	ar := r.active[sessionID]
	r.mu.RUnlock()
	if ar == nil {
		return false
	}
	ar.cancel()
	return true
}

// Start launches a new investigation in a background goroutine.
// localPcapPath is the host-side path to the pcap file (may be empty if pcap is in DB).
func (r *Runner) Start(
	parentCtx context.Context,
	sessionID, query, localPcapPath string,
	startRound, endRound int,
) error {
	r.mu.Lock()
	if _, exists := r.active[sessionID]; exists {
		r.mu.Unlock()
		return fmt.Errorf("session %s already running", sessionID)
	}

	ctx, cancel := context.WithCancel(parentCtx)
	ar := &activeRun{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	r.active[sessionID] = ar
	r.mu.Unlock()

	go r.run(ar, sessionID, query, localPcapPath, startRound, endRound)
	return nil
}

func (r *Runner) run(ar *activeRun, sessionID, query, localPcapPath string, startRound, endRound int) {
	defer func() {
		ar.closeClients()
		r.mu.Lock()
		delete(r.active, sessionID)
		r.mu.Unlock()
		close(ar.done)
	}()

	ctx := ar.ctx

	// --- Emitter that broadcasts to SSE clients + DB ---
	emitter := events.NewChannelEmitter(1024)
	log := logger.NewLogger().
		WithSink(logger.NewPrettyConsoleSink()).
		WithEmit(func(eventType string, data any) {
			ev := events.NewEvent(eventType, sessionID, data)
			ar.broadcast(r.store, sessionID, ev)
		})
	logger.SetDefault(log)

	// Forward emitter events too (these come from planner/executor via log.Emit).
	sub := emitter.Subscribe()
	go func() {
		for range sub {
			// Events already handled via log.WithEmit above; just drain.
		}
	}()
	defer emitter.Close()

	_ = r.store.UpdateSessionStatus(ctx, sessionID, "running")

	// --- LLM ---
	apiKey := os.Getenv("OPENAI_API_KEY")
	modelName := os.Getenv("OPENAI_MODEL_NAME")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if apiKey == "" || modelName == "" {
		r.emitError(ar, sessionID, "init", "OPENAI_API_KEY and OPENAI_MODEL_NAME required")
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  apiKey,
		Model:   modelName,
		BaseURL: baseURL,
	})
	if err != nil {
		r.emitError(ar, sessionID, "init", fmt.Sprintf("create chat model: %v", err))
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	// --- Docker sandbox ---
	log.Info(ctx, "creating docker sandbox...")
	env, err := environment.NewDockerEnv(ctx)
	if err != nil {
		r.emitError(ar, sessionID, "init", fmt.Sprintf("create docker env: %v", err))
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}
	defer env.Cleanup(context.Background())

	// --- Tools ---
	bashTool := tools.NewOutputGuard(tools.NewBashTool(env))
	sreTool := tools.NewSafeStrReplaceEditor(ctx, env)
	allTools := []tool.BaseTool{bashTool, sreTool}

	// --- Conversation summariser ---
	convSummarizer, err := summarizer.NewDefaultConversationSummarizer(ctx, &summarizer.Config{Model: chatModel}, log)
	if err != nil {
		r.emitError(ar, sessionID, "init", fmt.Sprintf("create summarizer: %v", err))
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	// --- Logger callback ---
	loggerCB := logger.NewLoggerCallback(log)
	callbacks.AppendGlobalHandlers(loggerCB)

	// --- ReAct agents ---
	execAgent, err := investigation.NewReActAgent(ctx, chatModel, allTools, 200, convSummarizer.SummarizeContext)
	if err != nil {
		r.emitError(ar, sessionID, "init", fmt.Sprintf("create executor agent: %v", err))
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	plannerAgent, err := investigation.NewReActAgent(ctx, chatModel, allTools, 15, nil)
	if err != nil {
		r.emitError(ar, sessionID, "init", fmt.Sprintf("create planner agent: %v", err))
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	p, err := planner.NewPlanner(ctx, plannerAgent, log)
	if err != nil {
		r.emitError(ar, sessionID, "init", fmt.Sprintf("create planner: %v", err))
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	exec := executor.NewExecutor(execAgent, chatModel, log)
	comp := summarizer.NewHistoryCompressor(chatModel, log)

	// --- Copy pcap to container ---
	sess, err := r.store.GetSession(ctx, sessionID)
	if err != nil {
		r.emitError(ar, sessionID, "init", fmt.Sprintf("load session: %v", err))
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	containerPcapPath, err := investigation.CopyPcapToContainer(ctx, env, sess, r.store, localPcapPath)
	if err != nil {
		r.emitError(ar, sessionID, "init", fmt.Sprintf("copy pcap to container: %v", err))
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}
	log.Info(ctx, "pcap copied to container", logger.A("path", containerPcapPath))

	// --- Emit lifecycle events ---
	log.Emit(events.TypeAnalysisStarted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: endRound,
	})

	// --- Investigation loop ---
	if err := investigation.RunInvestigation(ctx, log, p, exec, comp, r.store, sessionID, query, containerPcapPath, startRound, endRound); err != nil {
		log.Emit(events.TypeError, events.ErrorData{Phase: "investigation", Message: err.Error()})
		_ = r.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	log.Emit(events.TypeAnalysisCompleted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: endRound,
	})

	_ = r.store.UpdateSessionStatus(ctx, sessionID, "completed")
}

func (r *Runner) emitError(ar *activeRun, sessionID, phase, msg string) {
	r.log.Error(context.Background(), fmt.Sprintf("[%s] %s: %s", sessionID, phase, msg))
	ev := events.NewEvent(events.TypeError, sessionID, events.ErrorData{
		Phase:   phase,
		Message: msg,
	})
	ar.broadcast(r.store, sessionID, ev)
}

// WaitDone blocks until the session's investigation goroutine exits.
// Returns immediately if the session is not running.
func (r *Runner) WaitDone(sessionID string) {
	ar := r.GetActive(sessionID)
	if ar == nil {
		return
	}
	<-ar.done
}

// marshalJSON is a helper to marshal data to a JSON string.
func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
