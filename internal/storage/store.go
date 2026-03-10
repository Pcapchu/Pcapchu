package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Pcapchu/Pcapchu/internal/common"
	"github.com/jmoiron/sqlx"

	_ "modernc.org/sqlite"
)

// Store persists pcap files, sessions, and investigation rounds to SQLite.
type Store struct {
	db *sqlx.DB
}

const ddl = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS pcap_files (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    filename   TEXT    NOT NULL,
    size       INTEGER NOT NULL,
    sha256     TEXT    NOT NULL UNIQUE,
    data       BLOB    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS sessions (
    id               TEXT PRIMARY KEY,
    user_query       TEXT NOT NULL,
    pcap_file_id     INTEGER REFERENCES pcap_files(id) ON DELETE SET NULL,
    pcap_path        TEXT,
    findings_summary TEXT NOT NULL DEFAULT '',
    report_summary   TEXT NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at       DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS rounds (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id        TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    round             INTEGER NOT NULL,
    research_findings TEXT NOT NULL DEFAULT '',
    operation_log     TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    key_findings      TEXT NOT NULL DEFAULT '',
    open_questions    TEXT NOT NULL DEFAULT '',
    markdown_report   TEXT NOT NULL DEFAULT '',
    compressed        INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(session_id, round)
);

CREATE TABLE IF NOT EXISTS history_snapshots (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id       TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    scope            TEXT NOT NULL,
    compressed_up_to INTEGER NOT NULL,
    content          TEXT NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(session_id, scope)
);

CREATE TABLE IF NOT EXISTS session_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    data       TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(session_id, seq)
);
`

// migrations runs idempotent schema migrations for columns added after the initial DDL.
const migrations = `
ALTER TABLE sessions ADD COLUMN status TEXT NOT NULL DEFAULT 'idle';
ALTER TABLE rounds ADD COLUMN markdown_report TEXT NOT NULL DEFAULT '';
`

// New opens (or creates) the SQLite database at path and initialises the schema.
func New(path string) (*Store, error) {
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	// Run idempotent migrations (ignore "duplicate column" errors).
	for _, stmt := range strings.Split(migrations, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt != "" {
			_, _ = db.Exec(stmt)
		}
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ===================================================================
// Pcap files
// ===================================================================

// InsertPcapFile stores a pcap blob. If an identical SHA-256 already exists
// the existing row's ID is returned (dedup).
func (s *Store) InsertPcapFile(ctx context.Context, filename string, data []byte) (int64, error) {
	hash := sha256Sum(data)

	// Check for existing identical blob.
	var existing int64
	err := s.db.QueryRowxContext(ctx,
		`SELECT id FROM pcap_files WHERE sha256 = ?`, hash).Scan(&existing)
	if err == nil {
		return existing, nil
	}

	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO pcap_files (filename, size, sha256, data, created_at) VALUES (?, ?, ?, ?, ?)`,
		filename, int64(len(data)), hash, data, now)
	if err != nil {
		return 0, fmt.Errorf("insert pcap file: %w", err)
	}
	return res.LastInsertId()
}

// GetPcapFileData returns the raw blob bytes for a pcap file.
func (s *Store) GetPcapFileData(ctx context.Context, id int64) ([]byte, error) {
	var data []byte
	if err := s.db.QueryRowxContext(ctx,
		`SELECT data FROM pcap_files WHERE id = ?`, id).Scan(&data); err != nil {
		return nil, fmt.Errorf("get pcap data: %w", err)
	}
	return data, nil
}

// GetPcapFilename returns the original filename for a stored pcap file.
func (s *Store) GetPcapFilename(ctx context.Context, id int64) (string, error) {
	var name string
	if err := s.db.QueryRowxContext(ctx,
		`SELECT filename FROM pcap_files WHERE id = ?`, id).Scan(&name); err != nil {
		return "", fmt.Errorf("get pcap filename: %w", err)
	}
	return name, nil
}

// ListPcapFiles returns metadata for all stored pcap files (no blob data).
func (s *Store) ListPcapFiles(ctx context.Context) ([]PcapListItem, error) {
	var items []PcapListItem
	if err := s.db.SelectContext(ctx, &items,
		`SELECT id, filename, size, sha256, created_at FROM pcap_files ORDER BY created_at DESC`); err != nil {
		return nil, fmt.Errorf("list pcap files: %w", err)
	}
	return items, nil
}

// DeletePcapFile removes a pcap file blob. Sessions referencing it will have
// pcap_file_id set to NULL (ON DELETE SET NULL).
func (s *Store) DeletePcapFile(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM pcap_files WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete pcap file: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("pcap file %d not found", id)
	}
	return nil
}

// ===================================================================
// Sessions
// ===================================================================

// CreateSession inserts a new session record.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_query, pcap_file_id, pcap_path, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserQuery, sess.PcapFileID, sess.PcapPath, now, now)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSession loads a single session by ID.
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	var sess Session
	if err := s.db.GetContext(ctx, &sess,
		`SELECT id, user_query, pcap_file_id, pcap_path, findings_summary, report_summary, status, created_at, updated_at
		 FROM sessions WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &sess, nil
}

// ListSessions returns all sessions with a round count, most recent first.
func (s *Store) ListSessions(ctx context.Context) ([]SessionListItem, error) {
	var items []SessionListItem
	if err := s.db.SelectContext(ctx, &items, `
		SELECT s.id, s.user_query, s.status, s.pcap_file_id, s.pcap_path,
		       s.created_at, s.updated_at,
		       COALESCE(r.cnt, 0) AS round_count
		FROM sessions s
		LEFT JOIN (SELECT session_id, COUNT(*) AS cnt FROM rounds GROUP BY session_id) r
		  ON r.session_id = s.id
		ORDER BY s.updated_at DESC`); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return items, nil
}

// DeleteSession removes a session and its rounds (ON DELETE CASCADE).
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	// Ensure FK enforcement for this connection.
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable FK: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session %s not found", id)
	}
	return nil
}

// TouchSession bumps updated_at for the given session.
func (s *Store) TouchSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET updated_at = ? WHERE id = ?`, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// UpdateSessionPcap re-attaches a pcap file to a session by pcap_file_id.
// Pass 0 to clear the pcap reference and use pcapPath instead.
func (s *Store) UpdateSessionPcap(ctx context.Context, sessionID string, pcapFileID int64, pcapPath string) error {
	now := time.Now().UTC()
	var err error
	if pcapFileID > 0 {
		_, err = s.db.ExecContext(ctx,
			`UPDATE sessions SET pcap_file_id = ?, pcap_path = NULL, updated_at = ? WHERE id = ?`,
			pcapFileID, now, sessionID)
	} else {
		_, err = s.db.ExecContext(ctx,
			`UPDATE sessions SET pcap_file_id = NULL, pcap_path = ?, updated_at = ? WHERE id = ?`,
			pcapPath, now, sessionID)
	}
	if err != nil {
		return fmt.Errorf("update session pcap: %w", err)
	}
	return nil
}

// ===================================================================
// Rounds
// ===================================================================

// SaveRound inserts a round record for the given session, and bumps updated_at.
func (s *Store) SaveRound(ctx context.Context, sessionID string, r Round) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO rounds (session_id, round, research_findings, operation_log, summary, key_findings, open_questions, markdown_report, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, r.Round, r.ResearchFindings, r.OperationLog,
		r.Summary, r.KeyFindings, r.OpenQuestions, r.MarkdownReport, now)
	if err != nil {
		return fmt.Errorf("save round: %w", err)
	}
	// Keep session timestamp fresh.
	_ = s.TouchSession(ctx, sessionID)
	return nil
}

// LoadHistory builds a SessionHistory from all stored rounds for a session.
// Compressed rounds contribute only through the session-level summaries.
func (s *Store) LoadHistory(ctx context.Context, sessionID string) (*common.SessionHistory, error) {
	// 1. Load session-level compressed summaries.
	var sess Session
	if err := s.db.GetContext(ctx, &sess,
		`SELECT findings_summary, report_summary FROM sessions WHERE id = ?`, sessionID); err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	// 2. Load all non-compressed rounds, ordered by round number.
	var rounds []Round
	if err := s.db.SelectContext(ctx, &rounds,
		`SELECT round, research_findings, operation_log, summary, key_findings, open_questions, markdown_report
		 FROM rounds WHERE session_id = ? AND compressed = 0 ORDER BY round ASC`, sessionID); err != nil {
		return nil, fmt.Errorf("load rounds: %w", err)
	}

	var (
		findingsParts []string
		opLogParts    []string
		allReports    []common.RoundReport
		lastReport    *common.RoundReport
	)

	if sess.FindingsSummary != "" {
		findingsParts = append(findingsParts, "## Compressed Summary of Earlier Rounds\n\n"+sess.FindingsSummary)
	}
	if sess.ReportSummary != "" {
		allReports = append(allReports, common.RoundReport{
			Round:   0,
			Summary: sess.ReportSummary,
		})
	}

	for _, r := range rounds {
		if r.ResearchFindings != "" {
			findingsParts = append(findingsParts, fmt.Sprintf("### Round %d\n%s", r.Round, r.ResearchFindings))
		}
		if r.OperationLog != "" {
			opLogParts = append(opLogParts, fmt.Sprintf("[Round %d]\n%s", r.Round, r.OperationLog))
		}

		rr := common.RoundReport{
			Round:         r.Round,
			Summary:       r.Summary,
			KeyFindings:   r.KeyFindings,
			OpenQuestions: r.OpenQuestions,
		}
		allReports = append(allReports, rr)
		lastReport = &rr
	}

	return &common.SessionHistory{
		Findings:       strings.Join(findingsParts, "\n\n"),
		OperationLog:   strings.Join(opLogParts, "\n---\n"),
		PreviousReport: lastReport,
		AllReports:     allReports,
	}, nil
}

// MarkCompressed marks all rounds up to and including upToRound as compressed.
func (s *Store) MarkCompressed(ctx context.Context, sessionID string, upToRound int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE rounds SET compressed = 1 WHERE session_id = ? AND round <= ?`,
		sessionID, upToRound)
	if err != nil {
		return fmt.Errorf("mark compressed: %w", err)
	}
	return nil
}

// SaveCompressedSummaries stores the compressed findings and report summaries at session level.
func (s *Store) SaveCompressedSummaries(ctx context.Context, sessionID string, findingsSummary, reportSummary string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET findings_summary = ?, report_summary = ? WHERE id = ?`,
		findingsSummary, reportSummary, sessionID)
	if err != nil {
		return fmt.Errorf("save compressed summaries: %w", err)
	}
	return nil
}

// RoundCount returns the number of rounds for a session.
func (s *Store) RoundCount(ctx context.Context, sessionID string) (int, error) {
	var count int
	if err := s.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM rounds WHERE session_id = ?`, sessionID); err != nil {
		return 0, fmt.Errorf("count rounds: %w", err)
	}
	return count, nil
}

// ===================================================================
// History Snapshots
// ===================================================================

// SaveSnapshot upserts a compressed history snapshot for the given session and scope.
func (s *Store) SaveSnapshot(ctx context.Context, sessionID, scope string, compressedUpTo int, content string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO history_snapshots (session_id, scope, compressed_up_to, content, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, scope) DO UPDATE SET
		   compressed_up_to = excluded.compressed_up_to,
		   content = excluded.content,
		   created_at = excluded.created_at`,
		sessionID, scope, compressedUpTo, content, now)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

// LoadSnapshot loads a compressed history snapshot. Returns nil if none exists.
func (s *Store) LoadSnapshot(ctx context.Context, sessionID, scope string) (*HistorySnapshot, error) {
	var snap HistorySnapshot
	err := s.db.GetContext(ctx, &snap,
		`SELECT id, session_id, scope, compressed_up_to, content, created_at
		 FROM history_snapshots WHERE session_id = ? AND scope = ?`, sessionID, scope)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	return &snap, nil
}

// LoadRoundsAfter loads rounds with round > afterRound, ordered ascending.
func (s *Store) LoadRoundsAfter(ctx context.Context, sessionID string, afterRound int) ([]Round, error) {
	var rounds []Round
	if err := s.db.SelectContext(ctx, &rounds,
		`SELECT round, research_findings, operation_log, summary, key_findings, open_questions, markdown_report
		 FROM rounds WHERE session_id = ? AND round > ? ORDER BY round ASC`,
		sessionID, afterRound); err != nil {
		return nil, fmt.Errorf("load rounds after %d: %w", afterRound, err)
	}
	return rounds, nil
}

// ===================================================================
// Session Events (for SSE replay)
// ===================================================================

// SaveEvent stores a single event for a session with monotonic sequence number.
func (s *Store) SaveEvent(ctx context.Context, sessionID string, seq int, eventType, data string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO session_events (session_id, seq, event_type, data, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		sessionID, seq, eventType, data, now)
	if err != nil {
		return fmt.Errorf("save event: %w", err)
	}
	return nil
}

// LoadSessionEvents returns all events for a session, ordered by seq.
func (s *Store) LoadSessionEvents(ctx context.Context, sessionID string) ([]SessionEvent, error) {
	var evts []SessionEvent
	if err := s.db.SelectContext(ctx, &evts,
		`SELECT id, session_id, seq, event_type, data, created_at
		 FROM session_events WHERE session_id = ? ORDER BY seq ASC`, sessionID); err != nil {
		return nil, fmt.Errorf("load session events: %w", err)
	}
	return evts, nil
}

// LoadSessionEventsSince returns events with seq > afterSeq.
func (s *Store) LoadSessionEventsSince(ctx context.Context, sessionID string, afterSeq int) ([]SessionEvent, error) {
	var evts []SessionEvent
	if err := s.db.SelectContext(ctx, &evts,
		`SELECT id, session_id, seq, event_type, data, created_at
		 FROM session_events WHERE session_id = ? AND seq > ? ORDER BY seq ASC`,
		sessionID, afterSeq); err != nil {
		return nil, fmt.Errorf("load events since seq %d: %w", afterSeq, err)
	}
	return evts, nil
}

// UpdateSessionStatus sets the status field for a session.
func (s *Store) UpdateSessionStatus(ctx context.Context, sessionID, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UTC(), sessionID)
	if err != nil {
		return fmt.Errorf("update session status: %w", err)
	}
	return nil
}

// UpdateSessionQuery sets the user_query field for a session.
func (s *Store) UpdateSessionQuery(ctx context.Context, sessionID, query string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET user_query = ?, updated_at = ? WHERE id = ?`,
		query, time.Now().UTC(), sessionID)
	if err != nil {
		return fmt.Errorf("update session query: %w", err)
	}
	return nil
}

// LoadRounds returns all rounds for a session, ordered by round number.
func (s *Store) LoadRounds(ctx context.Context, sessionID string) ([]Round, error) {
	var rounds []Round
	if err := s.db.SelectContext(ctx, &rounds,
		`SELECT id, session_id, round, research_findings, operation_log, summary, key_findings, open_questions, markdown_report, compressed, created_at
		 FROM rounds WHERE session_id = ? ORDER BY round ASC`, sessionID); err != nil {
		return nil, fmt.Errorf("load rounds: %w", err)
	}
	return rounds, nil
}

// ===================================================================
// Helpers
// ===================================================================

func sha256Sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// NullInt64 returns a valid sql.NullInt64 with the given value.
func NullInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

// NullString returns a valid sql.NullString with the given value.
func NullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: true}
}
