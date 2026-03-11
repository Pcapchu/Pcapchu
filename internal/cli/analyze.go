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
	analyzePcap    string
	analyzeQuery   string
	analyzeRounds  int
	analyzeSession string
)

func init() {
	analyzeCmd.Flags().StringVar(&analyzePcap, "pcap", "", "path to local .pcap file (required for new session)")
	analyzeCmd.Flags().StringVar(&analyzeQuery, "query", "Analyze this pcap file and identify any security concerns.", "analysis query")
	analyzeCmd.Flags().IntVar(&analyzeRounds, "rounds", 1, "number of investigation rounds")
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

	rt, err := newCLIRuntime(context.Background())
	if err != nil {
		return err
	}
	defer rt.Close()

	sessionID := uuid.New().String()

	// Always store pcap in DB.
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read pcap file: %w", err)
	}
	pcapID, err := store.InsertPcapFile(rt.Ctx(), filepath.Base(absPath), data)
	if err != nil {
		return fmt.Errorf("store pcap: %w", err)
	}
	rt.Log().Info(rt.Ctx(), "pcap stored in database", logger.A("pcap_id", pcapID), logger.A("size", len(data)))

	var sess storage.Session
	sess.ID = sessionID
	sess.UserQuery = analyzeQuery
	sess.PcapFileID = storage.NullInt64(pcapID)

	rt.Log().Emit(events.TypePcapLoaded, events.PcapLoadedData{
		Source:   "db",
		Path:     "/home/linuxbrew/" + filepath.Base(absPath),
		Size:     int64(len(data)),
		Filename: filepath.Base(absPath),
	})

	if err := store.CreateSession(rt.Ctx(), sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// Copy pcap to container (always from local file for new analysis).
	containerPcapPath := "/home/linuxbrew/" + filepath.Base(absPath)
	if err := rt.Env().CopyFile(rt.Ctx(), absPath, containerPcapPath); err != nil {
		return fmt.Errorf("copy pcap to container: %w", err)
	}
	rt.Log().Info(rt.Ctx(), "pcap copied to container", logger.A("path", containerPcapPath))

	rt.Log().Emit(events.TypeSessionCreated, events.SessionCreatedData{
		SessionID:  sessionID,
		UserQuery:  analyzeQuery,
		PcapSource: "db",
	})

	rt.Log().Emit(events.TypeAnalysisStarted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: analyzeRounds,
	})

	rt.SetUserQuery(analyzeQuery)
	if err := investigation.RunInvestigation(rt.Ctx(), rt.Log(), rt.Planner(), rt.Exec(), rt.Compressor(), store, sessionID, analyzeQuery, containerPcapPath, 1, analyzeRounds); err != nil {
		return err
	}

	rt.Log().Emit(events.TypeAnalysisCompleted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: analyzeRounds,
	})

	rt.Log().Info(rt.Ctx(), "investigation complete", logger.A("session_id", sessionID))
	return nil
}

// resumeSession reloads pcap and continues investigation rounds.
func resumeSession(store *storage.Store, sessionID, queryOverride string, rounds int) error {
	rt, err := newCLIRuntime(context.Background())
	if err != nil {
		return err
	}
	defer rt.Close()

	sess, err := store.GetSession(rt.Ctx(), sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	query := sess.UserQuery
	if queryOverride != "" && queryOverride != "Analyze this pcap file and identify any security concerns." {
		query = queryOverride
	}

	containerPcapPath, err := investigation.CopyPcapToContainer(rt.Ctx(), rt.Env(), sess, store, "")
	if err != nil {
		return fmt.Errorf("load pcap into container: %w", err)
	}
	rt.Log().Info(rt.Ctx(), "pcap copied to container", logger.A("path", containerPcapPath))

	prevRounds, err := store.RoundCount(rt.Ctx(), sessionID)
	if err != nil {
		return fmt.Errorf("count previous rounds: %w", err)
	}
	startRound := prevRounds + 1
	endRound := startRound + rounds - 1

	rt.Log().Emit(events.TypeSessionResumed, events.SessionResumedData{
		SessionID: sessionID,
		FromRound: startRound,
	})

	rt.Log().Emit(events.TypeAnalysisStarted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: endRound,
	})

	rt.SetUserQuery(query)
	if err := investigation.RunInvestigation(rt.Ctx(), rt.Log(), rt.Planner(), rt.Exec(), rt.Compressor(), store, sessionID, query, containerPcapPath, startRound, endRound); err != nil {
		return err
	}

	rt.Log().Emit(events.TypeAnalysisCompleted, events.AnalysisData{
		SessionID:   sessionID,
		TotalRounds: endRound,
	})

	rt.Log().Info(rt.Ctx(), "investigation complete", logger.A("session_id", sessionID))
	return nil
}
