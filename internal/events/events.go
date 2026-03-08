package events

import (
	"encoding/json"
	"sync"
	"time"
)

// Event types for frontend consumption.
const (
	// Session lifecycle
	TypeSessionCreated = "session.created"
	TypeSessionResumed = "session.resumed"

	// Analysis lifecycle
	TypeAnalysisStarted   = "analysis.started"
	TypeAnalysisCompleted = "analysis.completed"

	// Pcap loading
	TypePcapLoaded = "pcap.loaded"

	// Round lifecycle
	TypeRoundStarted   = "round.started"
	TypeRoundCompleted = "round.completed"

	// Planner events
	TypePlanCreated = "plan.created"
	TypePlanError   = "plan.error"

	// Executor events
	TypeStepStarted   = "step.started"
	TypeStepFindings  = "step.findings"
	TypeStepCompleted = "step.completed"
	TypeStepError     = "step.error"

	// Final
	TypeReportGenerated = "report.generated"

	// General
	TypeInfo  = "info"
	TypeError = "error"
)

// Event is the unified event structure sent to consumers (CLI printer, SSE endpoint, etc.).
// Data is a json.RawMessage so consumers can decode it based on Type.
type Event struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// NewEvent creates an Event, marshaling data to JSON. If marshaling fails, data is set to null.
func NewEvent(eventType string, sessionID string, data any) Event {
	raw, err := json.Marshal(data)
	if err != nil {
		raw = []byte("null")
	}
	return Event{
		Type:      eventType,
		SessionID: sessionID,
		Timestamp: time.Now(),
		Data:      raw,
	}
}

// --- Typed event data structs (frontend-friendly JSON) ---

type SessionCreatedData struct {
	SessionID  string `json:"session_id"`
	UserQuery  string `json:"user_query"`
	PcapSource string `json:"pcap_source"` // "file" or "db"
}

type SessionResumedData struct {
	SessionID string `json:"session_id"`
	FromRound int    `json:"from_round"`
}

type AnalysisData struct {
	SessionID   string `json:"session_id"`
	TotalRounds int    `json:"total_rounds"`
}

type PcapLoadedData struct {
	Source   string `json:"source"` // "file" or "db"
	Path     string `json:"path"`   // container-side path
	Size     int64  `json:"size"`
	Filename string `json:"filename"`
}

type RoundStartedData struct {
	Round       int `json:"round"`
	TotalRounds int `json:"total_rounds"`
}

type RoundCompletedData struct {
	Round       int    `json:"round"`
	Summary     string `json:"summary"`
	KeyFindings string `json:"key_findings"`
}

type PlanCreatedData struct {
	Thought    string     `json:"thought"`
	TotalSteps int        `json:"total_steps"`
	Steps      []StepInfo `json:"steps"`
}

type StepInfo struct {
	StepID int    `json:"step_id"`
	Intent string `json:"intent"`
}

type StepStartedData struct {
	StepID     int    `json:"step_id"`
	Intent     string `json:"intent"`
	TotalSteps int    `json:"total_steps"`
}

type StepFindingsData struct {
	StepID   int    `json:"step_id"`
	Intent   string `json:"intent"`
	Findings string `json:"findings"`
	Actions  string `json:"actions"`
}

type StepCompletedData struct {
	StepID     int `json:"step_id"`
	TotalSteps int `json:"total_steps"`
}

type ReportData struct {
	Round      int    `json:"round"`
	Report     string `json:"report"`
	ContentLen int    `json:"content_length"`
	TotalSteps int    `json:"total_steps"`
	DurationMs int64  `json:"duration_ms"`
}

type ErrorData struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
	StepID  int    `json:"step_id,omitempty"`
}

type InfoData struct {
	Message string `json:"message"`
}

// --- Emitter interface and channel-based implementation ---

// Emitter is the interface for publishing events. Implementations may push to a channel,
// write to ES, or stream via SSE.
type Emitter interface {
	Emit(event Event)
	Subscribe() <-chan Event
	Close()
}

// ChannelEmitter is a buffered channel-based Emitter.
type ChannelEmitter struct {
	ch      chan Event
	subs    []chan Event
	mu      sync.RWMutex
	closed  bool
	bufSize int
}

// NewChannelEmitter creates a new emitter with the given buffer size.
func NewChannelEmitter(bufSize int) *ChannelEmitter {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &ChannelEmitter{
		ch:      make(chan Event, bufSize),
		bufSize: bufSize,
	}
}

// Emit publishes an event to all subscribers. Blocks until every subscriber receives the event.
func (e *ChannelEmitter) Emit(event Event) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return
	}
	for _, sub := range e.subs {
		sub <- event
	}
}

// Subscribe returns a channel that receives all emitted events.
func (e *ChannelEmitter) Subscribe() <-chan Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := make(chan Event, e.bufSize)
	e.subs = append(e.subs, ch)
	return ch
}

// Close closes all subscriber channels.
func (e *ChannelEmitter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	for _, sub := range e.subs {
		close(sub)
	}
}

// NopEmitter is a no-op emitter for when event reporting is not needed.
type NopEmitter struct{}

func (NopEmitter) Emit(Event)              {}
func (NopEmitter) Subscribe() <-chan Event { return make(chan Event) }
func (NopEmitter) Close()                  {}
