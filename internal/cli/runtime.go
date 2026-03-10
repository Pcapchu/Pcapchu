package cli

import (
	"context"
	"fmt"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/investigation"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
)

// cliRuntime wraps an investigation.Runtime with CLI-specific concerns:
// signal handling, OTel setup, and an event-printing goroutine.
type cliRuntime struct {
	*investigation.Runtime
	otelShutdown func(context.Context)
	cleanupOnce  sync.Once
}

// newCLIRuntime creates a Runtime suitable for CLI invocations.
// It adds SIGINT/SIGTERM handling and an OTel sink if configured.
func newCLIRuntime(parentCtx context.Context) (*cliRuntime, error) {
	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)

	// --- Build the top-level logger (single instance for the CLI process) ---
	parentLog, otelShutdown, err := logger.NewDefaultLogger(ctx, "pcapchu")
	if err != nil {
		cancel()
		return nil, fmt.Errorf("init logger: %w", err)
	}

	// --- Create emitter + wire logger's EmitFunc to it ---
	emitter := events.NewChannelEmitter(1024)
	log := logger.NewLogger().
		WithSink(parentLog.Sink()).
		WithEmit(func(eventType string, data any) {
			emitter.Emit(events.NewEvent(eventType, "", data))
		})

	// --- Create the investigation runtime ---
	rt, err := investigation.NewRuntime(ctx, log, emitter)
	if err != nil {
		cancel()
		emitter.Close()
		return nil, err
	}

	// --- Event printer goroutine (CLI console feedback) ---
	ch := rt.Emitter().Subscribe()
	go func() {
		for ev := range ch {
			fmt.Printf("[%s] %s: %s\n", ev.Timestamp.Format("15:04:05"), ev.Type, string(ev.Data))
		}
	}()

	logger.SetDefault(rt.Log())

	crt := &cliRuntime{
		Runtime:      rt,
		otelShutdown: otelShutdown,
	}

	// Ensure cleanup on signal (Close is Once-guarded so double-close is safe).
	go func() {
		<-ctx.Done()
		crt.Close()
	}()

	return crt, nil
}

func (r *cliRuntime) Close() {
	r.cleanupOnce.Do(func() {
		r.Runtime.Close()
		if r.otelShutdown != nil {
			r.otelShutdown(context.Background())
		}
	})
}
