package common

import (
	"encoding/json"
	"strings"
)

// Step represents a single execution step in the investigation plan.
type Step struct {
	StepID int    `json:"step_id"`
	Intent string `json:"intent"`
}

// Plan is the top-level structure returned by the Planner LLM.
type Plan struct {
	Thought       string `json:"thought"`
	EnrichedInput string `json:"enriched_input"`
	TableSchema   string `json:"table_schema"`
	Steps         []Step `json:"steps"`
}

// PlanState is the mutable state carried through the executor graph loop.
type PlanState struct {
	Plan               Plan
	TableSchema        string
	KeyFindingsHistory string
	CurrentStepIndex   int
	ResearchFindings   string
	OperationLog       []string
	EndOutput          string
}

// NormalOutput is the parsed JSON output from a NormalExecutor step.
type NormalOutput struct {
	Findings  FlexString `json:"findings"`
	MyActions FlexString `json:"my_actions"`
}

// FlexString handles LLM returning either a string or []string, unifying to string.
type FlexString string

func (f *FlexString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*f = FlexString(strings.Join(arr, "\n"))
		return nil
	}
	*f = FlexString(string(data))
	return nil
}

func (f FlexString) String() string {
	return string(f)
}

// RoundSummary is the parsed JSON output from the Final Executor (phased summary).
type RoundSummary struct {
	Summary        FlexString `json:"summary"`
	KeyFindings    FlexString `json:"key_findings"`
	OpenQuestions   FlexString `json:"open_questions"`
	MarkdownReport FlexString `json:"markdown_report"`
}

// RoundReport is a completed round summary tagged with its round number.
type RoundReport struct {
	Round          int    `json:"round"`
	Summary        string `json:"summary"`
	KeyFindings    string `json:"key_findings"`
	OpenQuestions   string `json:"open_questions"`
	MarkdownReport string `json:"markdown_report"`
}

// SessionHistory holds accumulated context from previous rounds, fed into the next planner.
type SessionHistory struct {
	Findings       string        // Cumulative research findings from all previous rounds
	OperationLog   string        // Concatenated operation logs from all previous rounds
	PreviousReport *RoundReport  // The most recent round report
	AllReports     []RoundReport // All reports from all rounds (tagged with round numbers)
}
