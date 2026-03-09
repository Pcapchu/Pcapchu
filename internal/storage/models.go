package storage

import (
	"database/sql"
	"time"
)

// PcapFile represents a row in the pcap_files table.
type PcapFile struct {
	ID        int64     `db:"id"`
	Filename  string    `db:"filename"`
	Size      int64     `db:"size"`
	SHA256    string    `db:"sha256"`
	Data      []byte    `db:"data"`
	CreatedAt time.Time `db:"created_at"`
}

// Session represents a row in the sessions table.
// Exactly one of PcapFileID or PcapPath is non-nil.
type Session struct {
	ID              string         `db:"id"`
	UserQuery       string         `db:"user_query"`
	PcapFileID      sql.NullInt64  `db:"pcap_file_id"`
	PcapPath        sql.NullString `db:"pcap_path"`
	FindingsSummary string         `db:"findings_summary"`
	ReportSummary   string         `db:"report_summary"`
	Status          string         `db:"status"`
	CreatedAt       time.Time      `db:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
}

// Round represents a row in the rounds table.
type Round struct {
	ID               int64     `db:"id"`
	SessionID        string    `db:"session_id"`
	Round            int       `db:"round"`
	ResearchFindings string    `db:"research_findings"`
	OperationLog     string    `db:"operation_log"`
	Summary          string    `db:"summary"`
	KeyFindings      string    `db:"key_findings"`
	OpenQuestions    string    `db:"open_questions"`
	MarkdownReport   string    `db:"markdown_report"`
	Compressed       bool      `db:"compressed"`
	CreatedAt        time.Time `db:"created_at"`
}

// --- View models for CLI / API listing ---

// SessionListItem is a projection used by ListSessions.
type SessionListItem struct {
	ID         string         `db:"id"`
	UserQuery  string         `db:"user_query"`
	RoundCount int            `db:"round_count"`
	Status     string         `db:"status"`
	PcapFileID sql.NullInt64  `db:"pcap_file_id"`
	PcapPath   sql.NullString `db:"pcap_path"`
	CreatedAt  time.Time      `db:"created_at"`
	UpdatedAt  time.Time      `db:"updated_at"`
}

// PcapSource returns a human-readable string describing where the pcap lives.
func (s *SessionListItem) PcapSource() string {
	if s.PcapFileID.Valid {
		return "db"
	}
	if s.PcapPath.Valid {
		return s.PcapPath.String
	}
	return "unknown"
}

// PcapListItem is a projection used by ListPcapFiles (no blob data).
type PcapListItem struct {
	ID        int64     `db:"id"`
	Filename  string    `db:"filename"`
	Size      int64     `db:"size"`
	SHA256    string    `db:"sha256"`
	CreatedAt time.Time `db:"created_at"`
}

// HistorySnapshot stores a compressed snapshot of accumulated history entries.
// The "scope" field identifies what was compressed (e.g. "planner_history", "key_findings").
type HistorySnapshot struct {
	ID             int64     `db:"id"`
	SessionID      string    `db:"session_id"`
	Scope          string    `db:"scope"`
	CompressedUpTo int       `db:"compressed_up_to"` // round number up to which entries were compressed
	Content        string    `db:"content"`
	CreatedAt      time.Time `db:"created_at"`
}

// SessionEvent stores a single event emitted during a session, for SSE replay.
type SessionEvent struct {
	ID        int64     `db:"id"`
	SessionID string    `db:"session_id"`
	Seq       int       `db:"seq"`
	EventType string    `db:"event_type"`
	Data      string    `db:"data"`
	CreatedAt time.Time `db:"created_at"`
}
