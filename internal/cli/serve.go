package cli

import (
	"context"
	"sync"

	"github.com/Pcapchu/Pcapchu/internal/server"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP SSE API server",
	RunE:  runServe,
}

var serveAddr string

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", ":8080", "listen address (host:port)")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	store, err := openStore()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())

	log, otelShutdown, _ := logger.NewDefaultLogger(ctx, "pcapchu")

	// Unified cleanup: DB + OTel + context cancel, protected by once.
	cleanup := sync.OnceFunc(func() {
		cancel()
		store.Close()
		if otelShutdown != nil {
			otelShutdown(context.Background())
		}
	})
	defer cleanup()
	trapSignals(cleanup)

	// Reset any sessions left in "running" state from a previous crash.
	sessions, _ := store.ListSessions(ctx)
	for _, s := range sessions {
		if s.Status == "running" {
			_ = store.UpdateSessionStatus(ctx, s.ID, "interrupted")
		}
	}

	srv := server.New(store, log, serveAddr)
	return srv.ListenAndServe(ctx)
}
