package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/investigation"
	"github.com/Pcapchu/Pcapchu/internal/storage"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Start a new pcap analysis session",
	RunE:  runAnalyze,
}

var (
	analyzePcap      string
	analyzeQuery     string
	analyzeRounds    int
	analyzeStorePcap bool
	analyzeSession   string
)

func init() {
	analyzeCmd.Flags().StringVar(&analyzePcap, "pcap", "", "path to local .pcap file (required for new session)")
	analyzeCmd.Flags().StringVar(&analyzeQuery, "query", "Analyze this pcap file and identify any security concerns.", "analysis query")
	analyzeCmd.Flags().IntVar(&analyzeRounds, "rounds", 1, "number of investigation rounds")
	analyzeCmd.Flags().BoolVar(&analyzeStorePcap, "store-pcap", false, "persist pcap binary data into SQLite")
	analyzeCmd.Flags().StringVar(&analyzeSession, "session", "", "resume an existing session ID instead of creating a new one")
	rootCmd.AddCommand(analyzeCmd)
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	store, err := openStore()
	if err != nil {
		return err
	}
	defer store.Close()

	// Resume existing session?
	if analyzeSession != "" {
		return resumeSession(store, analyzeSession, analyzeQuery, analyzeRounds)
	}

	// New session requires --pcap.
	if analyzePcap == "" {
		return fmt.Errorf("--pcap is required (or use --session to resume)")
	}

	absPath, err := filepath.Abs(analyzePcap)
	if err != nil {
		return fmt.Errorf("resolve pcap path: %w", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("pcap file not accessible: %w", err)
	}

	rt, err := newRuntime(context.Background())
	if err != nil {
		return err
	}
	defer rt.Close()

	sessionID := uuid.New().String()

	var sess storage.Session
	sess.ID = sessionID
	sess.UserQuery = analyzeQuery

	if analyzeStorePcap {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return fmt.Errorf("read pcap file: %w", err)
		}
		pcapID, err := store.InsertPcapFile(rt.ctx, filepath.Base(absPath), data)
		if err != nil {
			return fmt.Errorf("store pcap: %w", err)
		}
		sess.PcapFileID = storage.NullInt64(pcapID)
		rt.log.Info(rt.ctx, "pcap stored in database", logger.A("pcap_id", pcapID), logger.A("size", len(data)))

		rt.log.Emit(events.TypePcapLoaded, events.PcapLoadedData{
			Source:   "db",
			Path:     "/home/linuxbrew/" + filepath.Base(absPath),
			Size:     int64(len(data)),
			Filename: filepath.Base(absPath),
		})
	} else {
		sess.PcapPath = storage.NullString(absPath)

		info, _ := os.Stat(absPath)
		var size int64
		if info != nil {
			size = info.Size()
		}
		rt.log.Emit(events.TypePcapLoaded, events.PcapLoadedData{
			Source:   "file",
			Path:     absPath,
			Size:     size,
			Filename: filepath.Base(absPath),
		})
	}

	if err := store.CreateSession(rt.ctx, sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// Copy pcap to container (always from local file for new analysis).
	containerPcapPath := "/home/linuxbrew/" + filepath.Base(absPath)
	if err := rt.env.CopyFile(rt.ctx, absPath, containerPcapPath); err != nil {
		return fmt.Errorf("copy pcap to container: %w", err)
	}
	rt.log.Info(rt.ctx, "pcap copied to container", logger.A("path", containerPcapPath))

	rt.log.Emit(events.TypeSessionCreated, events.SessionCreatedData{
		SessionID:  sessionID,
		UserQuery:  analyzeQuery,
		PcapSource: pcapSourceLabel(sess),
	})

	rt.log.Emit(events.TypeAnalysisStarted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: analyzeRounds,
	})

	if err := investigation.RunInvestigation(rt.ctx, rt.log, rt.planner, rt.exec, rt.compressor, store, sessionID, analyzeQuery, containerPcapPath, 1, analyzeRounds); err != nil {
		return err
	}

	rt.log.Emit(events.TypeAnalysisCompleted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: analyzeRounds,
	})

	rt.log.Info(rt.ctx, "investigation complete", logger.A("session_id", sessionID))
	return nil
}

// resumeSession reloads pcap and continues investigation rounds.
func resumeSession(store *storage.Store, sessionID, queryOverride string, rounds int) error {
	rt, err := newRuntime(context.Background())
	if err != nil {
		return err
	}
	defer rt.Close()

	sess, err := store.GetSession(rt.ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	query := sess.UserQuery
	if queryOverride != "" && queryOverride != "Analyze this pcap file and identify any security concerns." {
		query = queryOverride
	}

	containerPcapPath, err := investigation.CopyPcapToContainer(rt.ctx, rt.env, sess, store, "")
	if err != nil {
		return fmt.Errorf("load pcap into container: %w", err)
	}
	rt.log.Info(rt.ctx, "pcap copied to container", logger.A("path", containerPcapPath))

	prevRounds, err := store.RoundCount(rt.ctx, sessionID)
	if err != nil {
		return fmt.Errorf("count previous rounds: %w", err)
	}
	startRound := prevRounds + 1
	endRound := startRound + rounds - 1

	rt.log.Emit(events.TypeSessionResumed, events.SessionResumedData{
		SessionID: sessionID,
		FromRound: startRound,
	})

	rt.log.Emit(events.TypeAnalysisStarted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: endRound,
	})

	if err := investigation.RunInvestigation(rt.ctx, rt.log, rt.planner, rt.exec, rt.compressor, store, sessionID, query, containerPcapPath, startRound, endRound); err != nil {
		return err
	}

	rt.log.Emit(events.TypeAnalysisCompleted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: endRound,
	})

	rt.log.Info(rt.ctx, "investigation complete", logger.A("session_id", sessionID))
	return nil
}

func pcapSourceLabel(sess storage.Session) string {
	if sess.PcapFileID.Valid {
		return "db"
	}
	return "file"
}
