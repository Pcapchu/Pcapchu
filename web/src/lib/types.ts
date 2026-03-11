// API types matching the Pcapchu HTTP SSE API

// --- Session ---
export interface Session {
  id: string;
  session_title: string;
  round_count: number;
  status: SessionStatus;
  pcap_source: string;
  created_at: string;
  updated_at: string;
}

export type SessionStatus = "idle" | "running" | "completed" | "error" | "cancelled" | "interrupted";

/** Event entry within a round (from GET /api/sessions/{id}). */
export interface StoredEvent {
  seq: number;
  type: string;
  data: Record<string, unknown>;
  timestamp: string;
}

/** A single round with its user query and events. */
export interface SessionRound {
  round: number;
  user_query: string;
  events: StoredEvent[];
}

/** Response from GET /api/sessions/{id}. */
export interface SessionDetail {
  id: string;
  session_title: string;
  status: SessionStatus;
  round_count: number;
  rounds: SessionRound[];
  created_at: string;
  updated_at: string;
}

// --- Pcap ---
export interface PcapFile {
  id: number;
  filename: string;
  size: number;
  sha256: string;
  created_at: string;
}

// --- Upload ---
export interface UploadResponse {
  session_id: string;
  pcap_id: number;
  filename: string;
  size: number;
}

// --- SSE Events ---
export type EventType =
  | "session.created"
  | "session.resumed"
  | "analysis.completed"
  | "pcap.loaded"
  | "round.started"
  | "round.completed"
  | "plan.created"
  | "plan.error"
  | "step.started"
  | "step.findings"
  | "step.completed"
  | "step.error"
  | "report.generated"
  | "info"
  | "error"
  | "done";

export interface SSEEvent {
  seq: number;
  type: EventType;
  data: Record<string, unknown>;
  timestamp?: string;
}

// Event data shapes
export interface SessionCreatedData {
  session_id: string;
  user_query: string;
  pcap_source: string;
}

export interface AnalysisStartedData {
  session_id: string;
  total_rounds: number;
}

export interface RoundStartedData {
  round: number;
  total_rounds: number;
}

export interface RoundCompletedData {
  round: number;
  summary: string;
  key_findings: string;
  markdown_report?: string;
}

export interface PlanCreatedData {
  thought: string;
  total_steps: number;
  steps: Array<{ step_id: number; intent: string }>;
}

export interface StepStartedData {
  step_id: number;
  intent: string;
  total_steps: number;
}

export interface StepFindingsData {
  step_id: number;
  intent: string;
  findings: string;
  actions: string;
}

export interface StepCompletedData {
  step_id: number;
  total_steps: number;
}

export interface ReportGeneratedData {
  round: number;
  report: string;
  markdown_report: string;
  content_length: number;
  total_steps: number;
  duration_ms: number;
}

export interface ErrorData {
  phase: string;
  message: string;
  step_id?: number;
}
