package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Pcapchu/Pcapchu/internal/common"

	_ "modernc.org/sqlite"
)

// Store persists per-round investigation data to SQLite.
type Store struct {
	db *sql.DB
}

// RoundRecord is the data saved after each executor run.
type RoundRecord struct {
	Round            int
	ResearchFindings string
	OperationLog     string
	Summary          string
	KeyFindings      string
	OpenQuestions     string
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    user_query TEXT NOT NULL,
    pcap_path  TEXT NOT NULL,
    findings_summary TEXT NOT NULL DEFAULT '',
    report_summary   TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS rounds (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id        TEXT NOT NULL REFERENCES sessions(id),
    round             INTEGER NOT NULL,
    research_findings TEXT NOT NULL DEFAULT '',
    operation_log     TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    key_findings      TEXT NOT NULL DEFAULT '',
    open_questions    TEXT NOT NULL DEFAULT '',
    compressed        INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(session_id, round)
);
`

// New opens (or creates) the SQLite database at path and initializes the schema.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateSession inserts a new session record.
func (s *Store) CreateSession(ctx context.Context, id, userQuery, pcapPath string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_query, pcap_path, created_at) VALUES (?, ?, ?, ?)`,
		id, userQuery, pcapPath, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SaveRound inserts a round record for the given session.
func (s *Store) SaveRound(ctx context.Context, sessionID string, r RoundRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO rounds (session_id, round, research_findings, operation_log, summary, key_findings, open_questions, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, r.Round, r.ResearchFindings, r.OperationLog,
		r.Summary, r.KeyFindings, r.OpenQuestions, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("save round: %w", err)
	}
	return nil
}

// LoadHistory builds a SessionHistory from all stored rounds for a session.
// Compressed rounds contribute only through the session-level summaries.
func (s *Store) LoadHistory(ctx context.Context, sessionID string) (*common.SessionHistory, error) {
	// 1. Load session-level compressed summaries.
	var findingsSummary, reportSummary string
	err := s.db.QueryRowContext(ctx,
		`SELECT findings_summary, report_summary FROM sessions WHERE id = ?`, sessionID,
	).Scan(&findingsSummary, &reportSummary)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	// 2. Load all non-compressed rounds, ordered by round number.
	rows, err := s.db.QueryContext(ctx,
		`SELECT round, research_findings, operation_log, summary, key_findings, open_questions
		 FROM rounds WHERE session_id = ? AND compressed = 0 ORDER BY round ASC`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("load rounds: %w", err)
	}
	defer rows.Close()

	var (
		findingsParts []string
		opLogParts    []string
		allReports    []common.RoundReport
		lastReport    *common.RoundReport
	)

	// Prepend compressed summaries if present.
	if findingsSummary != "" {
		findingsParts = append(findingsParts, "## Compressed Summary of Earlier Rounds\n\n"+findingsSummary)
	}
	if reportSummary != "" {
		allReports = append(allReports, common.RoundReport{
			Round:   0, // sentinel: compressed rounds
			Summary: reportSummary,
		})
	}

	for rows.Next() {
		var r RoundRecord
		if err := rows.Scan(&r.Round, &r.ResearchFindings, &r.OperationLog,
			&r.Summary, &r.KeyFindings, &r.OpenQuestions); err != nil {
			return nil, fmt.Errorf("scan round: %w", err)
		}

		if r.ResearchFindings != "" {
			findingsParts = append(findingsParts, fmt.Sprintf("### Round %d\n%s", r.Round, r.ResearchFindings))
		}
		if r.OperationLog != "" {
			opLogParts = append(opLogParts, fmt.Sprintf("[Round %d]\n%s", r.Round, r.OperationLog))
		}

		rr := common.RoundReport{
			Round:        r.Round,
			Summary:      r.Summary,
			KeyFindings:  r.KeyFindings,
			OpenQuestions: r.OpenQuestions,
		}
		allReports = append(allReports, rr)
		lastReport = &rr
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rounds: %w", err)
	}

	return &common.SessionHistory{
		Findings:       strings.Join(findingsParts, "\n\n"),
		OperationLog:   strings.Join(opLogParts, "\n---\n"),
		PreviousReport: lastReport,
		AllReports:     allReports,
	}, nil
}

// MarkCompressed marks all rounds up to and including upToRound as compressed.
// Call this after compressing their content into session-level summaries.
func (s *Store) MarkCompressed(ctx context.Context, sessionID string, upToRound int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE rounds SET compressed = 1 WHERE session_id = ? AND round <= ?`,
		sessionID, upToRound,
	)
	if err != nil {
		return fmt.Errorf("mark compressed: %w", err)
	}
	return nil
}

// SaveCompressedSummaries stores the compressed findings and report summaries at session level.
// These replace the detailed content of rounds marked as compressed.
func (s *Store) SaveCompressedSummaries(ctx context.Context, sessionID string, findingsSummary, reportSummary string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET findings_summary = ?, report_summary = ? WHERE id = ?`,
		findingsSummary, reportSummary, sessionID,
	)
	if err != nil {
		return fmt.Errorf("save compressed summaries: %w", err)
	}
	return nil
}

// RoundCount returns the number of rounds for a session.
func (s *Store) RoundCount(ctx context.Context, sessionID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rounds WHERE session_id = ?`, sessionID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count rounds: %w", err)
	}
	return count, nil
}
