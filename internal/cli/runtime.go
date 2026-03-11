package cli

import (
	"context"
	"fmt"
	"sync"

	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/investigation"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
)

// cliRuntime wraps an investigation.Runtime with CLI-specific concerns:
// OTel setup and an event-printing goroutine.
// Caller is responsible for signal handling — see trapSignals().
type cliRuntime struct {
	*investigation.Runtime
	otelShutdown func(context.Context)
	cleanupOnce  sync.Once
}

// newCLIRuntime creates a Runtime suitable for CLI invocations.
// It sets up OTel, the event emitter, and an event-printing goroutine,
// but does NOT register signal handlers — the caller should use
// sync.OnceFunc + trapSignals for that.
func newCLIRuntime(parentCtx context.Context) (*cliRuntime, error) {
	parentLog, otelShutdown, err := logger.NewDefaultLogger(parentCtx, "pcapchu")
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}

	emitter := events.NewChannelEmitter(1024)
	log := logger.NewLogger().
		WithSink(parentLog.Sink()).
		WithEmit(func(eventType string, data any) {
			emitter.Emit(events.NewEvent(eventType, "", data))
		})

	rt, err := investigation.NewRuntime(parentCtx, log, emitter)
	if err != nil {
		if otelShutdown != nil {
			otelShutdown(context.Background())
		}
		return nil, err
	}

	// Event printer goroutine (CLI console feedback).
	ch := rt.Emitter().Subscribe()
	go func() {
		for ev := range ch {
			fmt.Printf("[%s] %s: %s\n", ev.Timestamp.Format("15:04:05"), ev.Type, string(ev.Data))
		}
	}()

	logger.SetDefault(rt.Log())

	return &cliRuntime{
		Runtime:      rt,
		otelShutdown: otelShutdown,
	}, nil
}

// Close tears down the runtime. Safe to call multiple times.
func (r *cliRuntime) Close() {
	r.cleanupOnce.Do(func() {
		r.Runtime.Close()
		if r.otelShutdown != nil {
			r.otelShutdown(context.Background())
		}
	})
}
